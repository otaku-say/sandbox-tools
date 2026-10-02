// sandbox-sdk-go — CubeSandbox 沙箱命令行客户端（官方 Go SDK，github.com/agent-infra/sandbox-sdk-go）。
//
// 设计目标：iSH（aarch64）上的标准沙箱控制面。
//   * 静态二进制，无解释器冷启动（Python SDK 每次调用要烧 ~7s 本机 CPU）
//   * 长任务走 async 通道：轮询封在进程内，对上层只算一次调用
//   * 保活走持久会话（平台底层即 tmux），而非 ad-hoc tmux
//
// 交叉编译（iSH 无 Go 工具链，在沙箱内构建）：
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o sandbox-sdk-go .
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
	"github.com/agent-infra/sandbox-sdk-go/option"
)

const (
	defaultDir  = "/home/gem"
	jobDir      = "/home/gem/jobs"
	httpTimeout = 180 * time.Second
	// version 与发布资产同步维护：https://github.com/otaku-say/sandbox-tools
	version = "4.0.0"
)

var (
	ctx          = context.Background()
	activeClient *http.Client
)

// ---------- 基础设施 ----------

func newClient() *client.Client {
	header := http.Header{}
	// 网关不校验时 SANDBOX_KEY 可留空（本部署的 CF 代理即如此）
	if key := os.Getenv("SANDBOX_KEY"); key != "" {
		header.Set("Authorization", "Bearer "+key)
	}
	// iSH 的模拟 CPU 上 TLS 握手可能超过 net/http 默认的 10s 上限，放宽并启用 SDK 重试。
	httpClient := &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: httpTimeout,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          10,
			ForceAttemptHTTP2:     true,
		},
	}
	activeClient = httpClient
	return client.NewClient(
		option.WithBaseURL(baseURL()),
		option.WithHTTPHeader(header),
		option.WithHTTPClient(httpClient),
		option.WithMaxAttempts(3),
	)
}

// rawGet 直连 REST 取原始 JSON。
// 用途：绕开 Go SDK 的严格时间戳解析——REST 返回 "2026-10-01T19:16:06.242026"（无时区），
// SDK 会报 `parsing time ... as "2006-01-02T15:04:05Z07:00"`，该方法不受影响。
func rawGet(path string) ([]byte, error) {
	if activeClient == nil {
		newClient()
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(baseURL(), "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if key := os.Getenv("SANDBOX_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := activeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s → HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] "+format+"\n", args...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fatal("%v", err)
	}
}

func arg(args []string, n int, usage string) string {
	if len(args) <= n {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	return args[n]
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func num(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func ptrBool(v bool) *bool       { return &v }
func ptrStr(v string) *string    { return &v }
func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int) *int          { return &v }

// shellQuote 生成可在 POSIX shell 中安全还原的单引号字面量。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isTerminal(status string) bool {
	switch status {
	case "completed", "failed", "killed", "timed_out":
		return true
	}
	return false
}

func statusOf(info *sdk.BashCommandInfo) string {
	if info == nil {
		return ""
	}
	return string(info.Status)
}

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(v)
		return
	}
	fmt.Println(string(data))
}

// ---------- 命令实现 ----------

// exec 同步执行：仅用于 <60s 的命令；>60s 会被网关掐断（空输出 + ~64s）。
// parseKVEnv 解析 --env=K=V,K2=V2 形式为 SDK 需要的 map[string]*string。
func parseKVEnv(s string) map[string]*string {
	if s == "" {
		return nil
	}
	out := map[string]*string{}
	for _, kv := range strings.Split(s, ",") {
		if i := strings.Index(kv, "="); i > 0 {
			v := kv[i+1:]
			out[kv[:i]] = &v
		}
	}
	return out
}

// cmdExec 支持 exec [--env=K=V,K2=V2] [--hard-timeout=秒] "<cmd>"。
// 环境变量只注入这一次调用，不落盘、不出现在其他进程里。
func cmdExec(c *client.Client, args []string) {
	rest := args[2:]
	var envs map[string]*string
	var hard *float64
	var parts []string
	for _, a := range rest {
		switch {
		case strings.HasPrefix(a, "--env="):
			envs = parseKVEnv(strings.TrimPrefix(a, "--env="))
			continue
		case strings.HasPrefix(a, "--hard-timeout="):
			if f, err := strconv.ParseFloat(strings.TrimPrefix(a, "--hard-timeout="), 64); err == nil {
				hard = &f
			}
			continue
		}
		parts = append(parts, a)
	}
	cmd := strings.Join(parts, " ")
	if cmd == "" {
		fatal("用法: sandbox-sdk-go exec [--env=K=V,K2=V2] \"<cmd>\"")
	}
	resp, err := c.Bash.Exec(ctx, &sdk.BashExecRequest{
		Command:     cmd,
		Env:         envs,
		HardTimeout: hard,
	})
	check(err)
	fmt.Print(str(resp.Data.Stdout))
	os.Exit(num(resp.Data.ExitCode))
}

// run 长任务：异步派发 + 按 offset 增量轮询，直到终态。
func cmdRun(c *client.Client, args []string) {
	cmd := arg(args, 2, "用法: sandbox-sdk-go run \"<cmd>\" [hard秒]")
	hard := 3600.0
	if len(args) > 3 {
		fmt.Sscanf(args[3], "%f", &hard)
	}
	resp, err := c.Bash.Exec(ctx, &sdk.BashExecRequest{
		Command: cmd, AsyncMode: ptrBool(true), HardTimeout: ptrFloat(hard),
	})
	check(err)
	session := resp.Data.SessionId
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] session=%s\n", session)

	offset := 0
	for {
		out, err := c.Bash.Output(ctx, &sdk.BashOutputRequest{
			SessionId: session, Offset: ptrInt(offset), Wait: ptrBool(true),
			WaitTimeout: ptrFloat(25),
		})
		check(err)
		fmt.Print(str(out.Data.Stdout))
		if out.Data.Offset != nil {
			offset = *out.Data.Offset
		}
		status := statusOf(out.Data.Command)
		if status == "" {
			time.Sleep(2 * time.Second) // 状态暂缺，避免空转
			continue
		}
		if isTerminal(status) {
			exit := 0
			if out.Data.Command != nil {
				exit = num(out.Data.Command.ExitCode)
			}
			fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] status=%s exit=%d\n", status, exit)
			if status != "completed" {
				os.Exit(1)
			}
			return
		}
	}
}

// sess 在持久会话中执行：工作目录与环境变量跨调用保持。
func cmdSess(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go sess <id> \"<cmd>\"")
	cmd := arg(args, 3, "用法: sandbox-sdk-go sess <id> \"<cmd>\"")
	resp, err := c.Shell.ExecCommand(ctx, &sdk.ShellExecRequest{Id: ptrStr(id), Command: cmd})
	check(err)
	fmt.Print(str(resp.Data.Output))
	os.Exit(num(resp.Data.ExitCode))
}

// sessnew 显式创建持久会话（已存在则复用）。
func cmdSessNew(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go sessnew <id> [dir]")
	dir := defaultDir
	if len(args) > 3 {
		dir = args[3]
	}
	if !ensureSession(c, id, dir) {
		fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 会话 %s 已存在，复用\n", id)
		return
	}
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 会话 %s 就绪\n", id)
}

func ensureSession(c *client.Client, id, dir string) bool {
	_, err := c.Shell.CreateSession(ctx, &sdk.ShellCreateSessionRequest{
		Id: ptrStr(id), ExecDir: ptrStr(dir),
	})
	return err == nil
}

func cmdSessions(c *client.Client, args []string) {
	body, err := rawGet("/v1/shell/sessions")
	check(err)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Println(pretty.String())
}

func cmdView(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go view <id>")
	resp, err := c.Shell.View(ctx, &sdk.ShellViewRequest{Id: id})
	check(err)
	for _, record := range resp.Data.Console {
		fmt.Printf("%s%s\n%s\n", record.Ps1, record.Command, str(record.Output))
	}
	if len(resp.Data.Console) == 0 {
		fmt.Print(resp.Data.Output)
	}
}

func cmdKill(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go kill <id>")
	_, err := c.Shell.CleanupSession(ctx, id)
	check(err)
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 会话 %s 已清理\n", id)
}

// job 保活后台任务：投进持久会话并 nohup，日志落盘，命令立即返回。
func cmdJob(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go job <id> \"<cmd>\"")
	cmd := arg(args, 3, "用法: sandbox-sdk-go job <id> \"<cmd>\"")
	if !ensureSession(c, id, defaultDir) {
		// 会话已存在，直接复用
	}
	log := fmt.Sprintf("%s/%s.log", jobDir, id)
	launch := fmt.Sprintf("mkdir -p %s && nohup bash -c %s >%s 2>&1 & echo \"pid=$! log=%s\"",
		jobDir, shellQuote(cmd), log, log)
	resp, err := c.Shell.ExecCommand(ctx, &sdk.ShellExecRequest{Id: ptrStr(id), Command: launch})
	check(err)
	fmt.Print(str(resp.Data.Output))
	fmt.Printf("[sandbox-sdk-go] 会话=%s 日志=%s\n", id, log)
}

func cmdLog(c *client.Client, args []string) {
	id := arg(args, 2, "用法: sandbox-sdk-go log <id> [行数]")
	lines := "40"
	if len(args) > 3 {
		lines = args[3]
	}
	execAndPrint(c, fmt.Sprintf("tail -n %s %s/%s.log 2>&1", lines, jobDir, id))
}

func cmdRead(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: sandbox-sdk-go read <远端路径>")
	resp, err := c.File.ReadFile(ctx, &sdk.FileReadRequest{File: remote})
	check(err)
	fmt.Print(resp.Data.Content)
}

// write 写文本/二进制：UTF-8 走明文，其余自动 base64。
func cmdWrite(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: sandbox-sdk-go write <远端路径> <本地文件>")
	local := arg(args, 3, "用法: sandbox-sdk-go write <远端路径> <本地文件>")
	content, err := os.ReadFile(local)
	check(err)
	request := &sdk.FileWriteRequest{File: remote}
	if utf8.Valid(content) {
		request.Content = string(content)
	} else {
		encoding := sdk.FileContentEncodingBase64
		request.Content = base64.StdEncoding.EncodeToString(content)
		request.Encoding = &encoding
	}
	resp, err := c.File.WriteFile(ctx, request)
	check(err)
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] %s ← %s (%d 字节)\n", remote, local, num(resp.Data.BytesWritten))
}

func cmdGet(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: sandbox-sdk-go get <远端路径> <本地文件>")
	local := arg(args, 3, "用法: sandbox-sdk-go get <远端路径> <本地文件>")
	reader, err := c.File.DownloadFile(ctx, &sdk.FileDownloadFileRequest{Path: remote})
	check(err)
	data, err := io.ReadAll(reader)
	check(err)
	check(os.WriteFile(local, data, 0o644))
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] %s → %s (%d 字节)\n", remote, local, len(data))
}

func cmdPS(c *client.Client, args []string) {
	execAndPrint(c, "ps -eo pid,etime,pcpu,rss,cmd --sort=-pcpu | head -20")
}

func baseURL() string {
	base := strings.TrimSuffix(os.Getenv("SANDBOX_BASE"), "/")
	if base == "" {
		fatal("SANDBOX_BASE 未设置（形如 https://<cubesandbox-proxy-host>/sandbox/<SID>/8080）")
	}
	return base
}

func cmdVersion(c *client.Client, args []string) {
	key := "未设置"
	if os.Getenv("SANDBOX_KEY") != "" {
		key = "已设置"
	}
	base := strings.TrimSuffix(os.Getenv("SANDBOX_BASE"), "/")
	if base == "" {
		base = "未设置（形如 https://<cubesandbox-proxy-host>/sandbox/<SID>/8080）"
	}
	fmt.Printf("sandbox-sdk-go %s (%s/%s, static)\nSANDBOX_BASE=%s\nSANDBOX_KEY=%s\n",
		version, runtime.GOOS, runtime.GOARCH, base, key)
}

func cmdHealth(c *client.Client, args []string) {
	execAndPrint(c, "hostname; date -Is; uptime; free -h | head -2; df -h / | tail -1")
}

func execAndPrint(c *client.Client, command string) {
	resp, err := c.Bash.Exec(ctx, &sdk.BashExecRequest{Command: command})
	check(err)
	fmt.Print(str(resp.Data.Stdout))
	os.Exit(num(resp.Data.ExitCode))
}

func usage() {
	usageWith(2)
}

func usageWith(code int) {
	fmt.Fprint(os.Stderr, `sandbox-sdk-go — CubeSandbox 沙箱客户端（Go SDK，静态二进制）
覆盖 agent-infra/sandbox 的全部 20 个命名空间 / 132 个方法

【基础命令】
  sandbox-sdk-go exec  [--env=K=V,K2=V2] "<cmd>"  同步执行（<60s）
      注意：--env 走 SDK 的 env 字段，实测本部署的 AIO 不消费它；
            传密钥请用"文件 + source"或"持久会话 export"（见技能文档）
  sandbox-sdk-go run   "<cmd>" [hard秒]        长任务：async 派发 + 增量轮询
  sandbox-sdk-go sess  <id> "<cmd>"           持久会话执行（cwd/env 跨调用保持）
  sandbox-sdk-go sessnew <id> [dir]           建持久会话
  sandbox-sdk-go sessions                     列出会话
  sandbox-sdk-go view  <id>                   查看会话控制台
  sandbox-sdk-go kill  <id>                   清理会话
  sandbox-sdk-go job   <id> "<cmd>"           保活后台任务（nohup + 日志）
  sandbox-sdk-go log   <id> [行数]            读任务日志
  sandbox-sdk-go read  <远端路径>              读远端文件到 stdout
  sandbox-sdk-go write <远端路径> <本地文件>    本地 → 远端
  sandbox-sdk-go get   <远端路径> <本地文件>    远端 → 本地
  sandbox-sdk-go ps / health / version        进程 / 体检 / 版本

【命名空间命令】sandbox-sdk-go <命名空间> <动作> [参数...]（不带动作时打印该空间用法）
  file     list read write replace search find grep glob upload download str-replace watch-list watch-create watch-events watch-poll watch-wait watch-stop
  code     run info
  jupyter  run info ls new rm rm-all
  nodejs   run info ls new get rm update
  util     markdown
  browser  info config restart screenshot action pac
  page     navigate back forward reload click fill type press hotkey hover select check uncheck upload fill-form scroll scroll-to scroll-to-element screenshot get-html get-text get-markdown elements console export-console evaluate find-text wait record
  tabs     ls new close activate
  cookies  ls set clear
  state    save load
  net      headers scoped-headers route-add route-rm requests har
  captcha  detect wait
  mcp      servers tools call
  skills   ls content register rm clear
  hooks    ls add rm
  proxy    ls add rm excludes exclude-add exclude-rm upstream upstream-set upstream-rm health diagnose
  display  record
  auth     ticket verify
  ctxinfo  context py-packages node-packages hooks
`)
	os.Exit(code)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	// 不依赖凭据的命令先行处理：未配置 SANDBOX_KEY 的环境也能查看版本与帮助。
	switch os.Args[1] {
	case "help", "-h", "--help":
		usageWith(0)
	case "version", "-v", "--version":
		cmdVersion(nil, os.Args)
		os.Exit(0)
	}
	c := newClient()
	// 命名空间式命令：sandbox-sdk-go <命名空间> <动作> [参数...]（见 dispatch.go）
	if fn, ok := namespaces[os.Args[1]]; ok {
		fn(c, os.Args[2:])
		return
	}
	switch os.Args[1] {
	case "exec":
		cmdExec(c, os.Args)
	case "run":
		cmdRun(c, os.Args)
	case "sess":
		cmdSess(c, os.Args)
	case "sessnew":
		cmdSessNew(c, os.Args)
	case "sessions":
		cmdSessions(c, os.Args)
	case "view":
		cmdView(c, os.Args)
	case "kill":
		cmdKill(c, os.Args)
	case "job":
		cmdJob(c, os.Args)
	case "log":
		cmdLog(c, os.Args)
	case "read":
		cmdRead(c, os.Args)
	case "write":
		cmdWrite(c, os.Args)
	case "get":
		cmdGet(c, os.Args)
	case "ps":
		cmdPS(c, os.Args)
	case "health":
		cmdHealth(c, os.Args)
	default:
		usage()
	}
}
