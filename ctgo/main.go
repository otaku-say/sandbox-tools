// ctgo — CT 沙箱命令行客户端（官方 Go SDK，github.com/agent-infra/sandbox-sdk-go）。
//
// 设计目标：iSH（aarch64）上的标准沙箱控制面。
//   * 静态二进制，无解释器冷启动（Python SDK 每次调用要烧 ~7s 本机 CPU）
//   * 长任务走 async 通道：轮询封在进程内，对上层只算一次调用
//   * 保活走持久会话（平台底层即 tmux），而非 ad-hoc tmux
//
// 交叉编译（iSH 无 Go 工具链，在沙箱内构建）：
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o ctgo .
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
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
	"github.com/agent-infra/sandbox-sdk-go/option"
)

const (
	defaultBase = "https://ct-sandbox-1.<private-host>"
	defaultDir  = "/home/gem"
	jobDir      = "/home/gem/jobs"
	httpTimeout = 180 * time.Second
	// version 与发布资产同步维护：https://github.com/otaku-say/sandbox-tools
	version = "1.0.0"
)

var (
	ctx          = context.Background()
	activeClient *http.Client
)

// ---------- 基础设施 ----------

func newClient() *client.Client {
	key := os.Getenv("CT_SANDBOX_1_KEY")
	if key == "" {
		fatal("CT_SANDBOX_1_KEY 未设置")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+key)
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
	req.Header.Set("Authorization", "Bearer "+os.Getenv("CT_SANDBOX_1_KEY"))
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
	fmt.Fprintf(os.Stderr, "[ctgo] "+format+"\n", args...)
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
func cmdExec(c *client.Client, args []string) {
	resp, err := c.Bash.Exec(ctx, &sdk.BashExecRequest{
		Command: arg(args, 2, "用法: ctgo exec \"<cmd>\""),
	})
	check(err)
	fmt.Print(str(resp.Data.Stdout))
	os.Exit(num(resp.Data.ExitCode))
}

// run 长任务：异步派发 + 按 offset 增量轮询，直到终态。
func cmdRun(c *client.Client, args []string) {
	cmd := arg(args, 2, "用法: ctgo run \"<cmd>\" [hard秒]")
	hard := 3600.0
	if len(args) > 3 {
		fmt.Sscanf(args[3], "%f", &hard)
	}
	resp, err := c.Bash.Exec(ctx, &sdk.BashExecRequest{
		Command: cmd, AsyncMode: ptrBool(true), HardTimeout: ptrFloat(hard),
	})
	check(err)
	session := resp.Data.SessionId
	fmt.Fprintf(os.Stderr, "[ctgo] session=%s\n", session)

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
			fmt.Fprintf(os.Stderr, "[ctgo] status=%s exit=%d\n", status, exit)
			if status != "completed" {
				os.Exit(1)
			}
			return
		}
	}
}

// sess 在持久会话中执行：工作目录与环境变量跨调用保持。
func cmdSess(c *client.Client, args []string) {
	id := arg(args, 2, "用法: ctgo sess <id> \"<cmd>\"")
	cmd := arg(args, 3, "用法: ctgo sess <id> \"<cmd>\"")
	resp, err := c.Shell.ExecCommand(ctx, &sdk.ShellExecRequest{Id: ptrStr(id), Command: cmd})
	check(err)
	fmt.Print(str(resp.Data.Output))
	os.Exit(num(resp.Data.ExitCode))
}

// sessnew 显式创建持久会话（已存在则复用）。
func cmdSessNew(c *client.Client, args []string) {
	id := arg(args, 2, "用法: ctgo sessnew <id> [dir]")
	dir := defaultDir
	if len(args) > 3 {
		dir = args[3]
	}
	if !ensureSession(c, id, dir) {
		fmt.Fprintf(os.Stderr, "[ctgo] 会话 %s 已存在，复用\n", id)
		return
	}
	fmt.Fprintf(os.Stderr, "[ctgo] 会话 %s 就绪\n", id)
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
	id := arg(args, 2, "用法: ctgo view <id>")
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
	id := arg(args, 2, "用法: ctgo kill <id>")
	_, err := c.Shell.CleanupSession(ctx, id)
	check(err)
	fmt.Fprintf(os.Stderr, "[ctgo] 会话 %s 已清理\n", id)
}

// job 保活后台任务：投进持久会话并 nohup，日志落盘，命令立即返回。
func cmdJob(c *client.Client, args []string) {
	id := arg(args, 2, "用法: ctgo job <id> \"<cmd>\"")
	cmd := arg(args, 3, "用法: ctgo job <id> \"<cmd>\"")
	if !ensureSession(c, id, defaultDir) {
		// 会话已存在，直接复用
	}
	log := fmt.Sprintf("%s/%s.log", jobDir, id)
	launch := fmt.Sprintf("mkdir -p %s && nohup bash -c %s >%s 2>&1 & echo \"pid=$! log=%s\"",
		jobDir, shellQuote(cmd), log, log)
	resp, err := c.Shell.ExecCommand(ctx, &sdk.ShellExecRequest{Id: ptrStr(id), Command: launch})
	check(err)
	fmt.Print(str(resp.Data.Output))
	fmt.Printf("[ctgo] 会话=%s 日志=%s\n", id, log)
}

func cmdLog(c *client.Client, args []string) {
	id := arg(args, 2, "用法: ctgo log <id> [行数]")
	lines := "40"
	if len(args) > 3 {
		lines = args[3]
	}
	execAndPrint(c, fmt.Sprintf("tail -n %s %s/%s.log 2>&1", lines, jobDir, id))
}

func cmdRead(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: ctgo read <远端路径>")
	resp, err := c.File.ReadFile(ctx, &sdk.FileReadRequest{File: remote})
	check(err)
	fmt.Print(resp.Data.Content)
}

// write 写文本/二进制：UTF-8 走明文，其余自动 base64。
func cmdWrite(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: ctgo write <远端路径> <本地文件>")
	local := arg(args, 3, "用法: ctgo write <远端路径> <本地文件>")
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
	fmt.Fprintf(os.Stderr, "[ctgo] %s ← %s (%d 字节)\n", remote, local, num(resp.Data.BytesWritten))
}

func cmdGet(c *client.Client, args []string) {
	remote := arg(args, 2, "用法: ctgo get <远端路径> <本地文件>")
	local := arg(args, 3, "用法: ctgo get <远端路径> <本地文件>")
	reader, err := c.File.DownloadFile(ctx, &sdk.FileDownloadFileRequest{Path: remote})
	check(err)
	data, err := io.ReadAll(reader)
	check(err)
	check(os.WriteFile(local, data, 0o644))
	fmt.Fprintf(os.Stderr, "[ctgo] %s → %s (%d 字节)\n", remote, local, len(data))
}

func cmdPS(c *client.Client, args []string) {
	execAndPrint(c, "ps -eo pid,etime,pcpu,rss,cmd --sort=-pcpu | head -20")
}

func baseURL() string {
	base := os.Getenv("CT_BASE")
	if base == "" {
		base = defaultBase
	}
	return base
}

func cmdVersion(c *client.Client, args []string) {
	key := "未设置"
	if os.Getenv("CT_SANDBOX_1_KEY") != "" {
		key = "已设置"
	}
	fmt.Printf("ctgo %s (%s/%s, static)\nCT_BASE=%s\nCT_SANDBOX_1_KEY=%s\n",
		version, runtime.GOOS, runtime.GOARCH, baseURL(), key)
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
	fmt.Fprint(os.Stderr, `ctgo — CT 沙箱客户端（Go SDK，aarch64 静态二进制）

  ctgo exec  "<cmd>"                同步执行（<60s）
  ctgo run   "<cmd>" [hard秒]        长任务：async 派发 + 增量轮询
  ctgo sess  <id> "<cmd>"           持久会话执行（cwd/env 跨调用保持）
  ctgo sessnew <id> [dir]           建持久会话
  ctgo sessions                     列出会话
  ctgo view  <id>                   查看会话控制台
  ctgo kill  <id>                   清理会话
  ctgo job   <id> "<cmd>"           保活后台任务（nohup + 日志）
  ctgo log   <id> [行数]            读任务日志
  ctgo read  <远端路径>              读远端文件到 stdout
  ctgo write <远端路径> <本地文件>    本地 → 远端
  ctgo get   <远端路径> <本地文件>    远端 → 本地
  ctgo ps                           进程列表
  ctgo health                       体检（主机/时间/负载/内存/磁盘）
  ctgo version                      版本与连接配置
`)
	os.Exit(code)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	// 不依赖凭据的命令先行处理：未配置 CT_SANDBOX_1_KEY 的环境也能查看版本与帮助。
	switch os.Args[1] {
	case "help", "-h", "--help":
		usageWith(0)
	case "version", "-v", "--version":
		cmdVersion(nil, os.Args)
		os.Exit(0)
	}
	c := newClient()
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
