// main.go —— CLI 入口与命令表（冻结文件，子代理请勿修改）
//
// 命名与风格约定：
//   - 需要取值的参数一律写成 --key=value（不支持 --key value，避免歧义）；
//   - 布尔开关写成 --flag；
//   - 所有业务输出走 stdout（人类可读或 JSON），错误走 stderr 且退出码非 0。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

type command struct {
	name  string
	usage string
	fn    func(args []string) error
}

// 命令表：命令名 → 用法 → 实现函数（实现散布在各 v2_*.go 中）
var commands = []command{
	{"version", "version", cmdVersion},
	{"health", "health", cmdHealth},
	{"sandbox-info", "sandbox-info", cmdSandboxInfo},
	{"sandbox-packages", "sandbox-packages --lang=python|node", cmdSandboxPackages},

	{"exec", "exec <命令> [--cwd=] [--env=K=V,K2=V2] [--timeout=] [--shell=] [--user=] [--max-output=] [--session=] | exec --id=<id> [--offset=] [--stderr-offset=]", cmdExec},
	{"async", "async <命令> [--cwd=] [--env=] [--user=]（mode=async，打印 command_id）", cmdAsync},
	{"log", "log <command_id> [--follow] [--interval=500ms] [--timeout=]", cmdLog},
	{"kill", "kill <command_id> [--signal=SIGKILL]", cmdKill},
	{"stdin", "stdin <command_id> <文本> [--enter]", cmdStdin},
	{"sess-new", "sess-new <session_id> [--cwd=] [--env=] [--user=]", cmdSessNew},
	{"sess", "sess <session_id> <命令> [--timeout=] [--max-output=]", cmdSess},
	{"sess-ls", "sess-ls", cmdSessLs},
	{"sess-rm", "sess-rm <session_id>", cmdSessRm},

	{"read", "read <path> [--start=] [--end=] [--user=]", cmdRead},
	{"cat", "cat <path> [--start=] [--end=]", cmdCat},
	{"write", "write <本地文件|-> <远端路径> [--append] [--user=]", cmdWrite},
	{"ls", "ls <path> [--recursive] [--hidden] [--depth=] [--user=]", cmdLs},
	{"stat", "stat <path> [--user=]", cmdStat},
	{"tree", "tree <path> [--depth=]", cmdTree},
	{"edit", "edit <path> --old=<旧串> --new=<新串> [--replace-all] | edit <path> --insert=<行号> --text=<内容>", cmdEdit},
	{"grep", "grep <path> <正则> [--fixed] [--ignore-case] [--include=*.go,*.py] [--max=]", cmdGrep},
	{"search", "search <path> <glob 如 **/*.py>", cmdSearch},
	{"mkdir", "mkdir <path> [--parents]", cmdMkdir},
	{"cp", "cp <源> <目标> [--overwrite]", cmdCp},
	{"mv", "mv <源> <目标> [--overwrite]", cmdMv},
	{"rm", "rm <路径> [--recursive]", cmdRm},
	{"put", "put <本地文件> <远端路径>（multipart 上传后移动到目标）", cmdPut},
	{"get", "get <远端路径> <本地文件>（二进制安全下载）", cmdGet},

	{"pty-new", "pty-new <会话id> [--cwd=] [--cols=] [--rows=] [--retention=persistent|expiring]", cmdPtyNew},
	{"pty", "pty <会话id> <命令> [--timeout=] [--async]", cmdPtyExec},
	{"pty-screen", "pty-screen <会话id>", cmdPtyScreen},
	{"pty-input", "pty-input <会话id> <文本> [--enter]", cmdPtyInput},
	{"pty-signal", "pty-signal <会话id> <信号，如 SIGINT>", cmdPtySignal},
	{"pty-resize", "pty-resize <会话id> --cols= --rows=", cmdPtyResize},
	{"pty-ls", "pty-ls", cmdPtyLs},
	{"pty-rm", "pty-rm <会话id>", cmdPtyRm},

	{"watch", "watch <路径> [--recursive] [--debounce=毫秒]", cmdWatch},
	{"watch-poll", "watch-poll <watcher_id> [--cursor=] [--timeout=] [--limit=]", cmdWatchPoll},
	{"watch-ls", "watch-ls", cmdWatchLs},
	{"watch-rm", "watch-rm <watcher_id>", cmdWatchRm},

	{"code", "code <源码> [--lang=python|javascript] [--session=] [--timeout=]", cmdCode},
	{"code-info", "code-info", cmdCodeInfo},
	{"code-sess-new", "code-sess-new [--lang=python|javascript]", cmdCodeSessNew},
	{"code-sess-ls", "code-sess-ls", cmdCodeSessLs},
	{"code-sess-rm", "code-sess-rm <session_id>", cmdCodeSessRm},

	{"br-info", "br-info", cmdBrInfo},
	{"br-go", "br-go <url> [--wait=load|domcontentloaded|networkidle] [--timeout=]", cmdBrGo},
	{"br-shot", "br-shot <输出文件.png> [--full] [--quality=0-100]", cmdBrShot},
	{"br-eval", "br-eval <表达式> [--await]", cmdBrEval},
	{"br-snapshot", "br-snapshot [--interactive]", cmdBrSnapshot},
	{"br-click", "br-click (--selector= | --ref=)", cmdBrClick},
	{"br-fill", "br-fill (--selector= | --ref=) --value=<内容>", cmdBrFill},
	{"br-tabs", "br-tabs", cmdBrTabs},
	{"br-tab-new", "br-tab-new [--url=]", cmdBrTabNew},
	{"br-tab-use", "br-tab-use <tab_id>", cmdBrTabUse},
	{"br-tab-close", "br-tab-close <tab_id>", cmdBrTabClose},
	{"br-cookies", "br-cookies [--url=] [--domain=]", cmdBrCookies},
	{"br-cookie-set", "br-cookie-set --name= --value= [--url= | --domain=]", cmdBrCookieSet},
	{"br-network", "br-network [--limit=] [--clear]", cmdBrNetwork},
	{"br-cdp", "br-cdp <CDP 方法，如 Browser.getVersion> [--params=JSON]", cmdBrCDP},

	{"mcp", "mcp <方法：initialize|tools/list|tools/call|ping> [--params=JSON]", cmdMCP},

	{"cmp-info", "cmp-info（computer-use；需 aio-computer 镜像，aio-daemon 上返回 503）", cmdCmpInfo},
	{"cmp-shot", "cmp-shot <输出文件.png>", cmdCmpShot},
	{"cmp-cursor", "cmp-cursor", cmdCmpCursor},
	{"cmp-clipboard", "cmp-clipboard", cmdCmpClipboard},
	{"cmp-windows", "cmp-windows", cmdCmpWindows},
	{"cmp-a11y", "cmp-a11y [--scope=] [--max-depth=] [--max-nodes=] [--role=] [--name=] [--match=] [--states=] [--include-offscreen=] [--timeout-ms=]", cmdCmpA11y},
	{"cmp-a11y-nodes", "cmp-a11y-nodes [同 cmp-a11y] [--limit=] [--node-id=]", cmdCmpA11yNodes},
	{"cmp-act", "cmp-act '<JSON 动作>' [--screenshot]（例：'{\"action\":\"click\",\"x\":100,\"y\":200}'）", cmdCmpAct},
	{"cmp-act-batch", "cmp-act-batch '<JSON 动作数组>' [--screenshot]", cmdCmpActBatch},
	{"cmp-record", "cmp-record [--action=start|stop] [--fps=] [--crf=] [--max-duration=] [--width=] [--height=] [--save-path=]", cmdCmpRecord},

	{"pty-ws", "pty-ws <会话id> [--protocol=json|binary] [--durable] [--restore] [--replay-bytes=]（WebSocket 附着终端，Ctrl-] 退出）", cmdPtyWS},
	{"pty-ws-anon", "pty-ws-anon [--protocol=json|binary]（匿名 WebShell，断开即销毁）", cmdPtyWSAnon},
	{"watch-events", "watch-events <watcher_id> [--max=]（SSE 事件流，Ctrl-C 退出）", cmdWatchEvents},

	{"br-upload", "br-upload (--selector= | --ref=) --paths=<沙箱内文件,...> [--tab-id=]（浏览器文件上传）", cmdBrUpload},
	{"br-config", "br-config [--resolution=1280x1024] | [--json='{...}']（浏览器配置）", cmdBrConfig},

	{"fs-tree-put", "fs-tree-put <本地 tar 文件|-> <远端目录> [--user=]（PUT /v2/fs/tree 整树上传）", cmdFsTreePut},
}

func cmdVersion(args []string) error {
	fmt.Printf("sandbox-sdk-go %s（纯 v2 API）\n", Version)
	if base := os.Getenv("SANDBOX_BASE"); base != "" {
		fmt.Printf("SANDBOX_BASE=%s\n", base)
	} else {
		fmt.Println("SANDBOX_BASE=(未设置)")
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "sandbox-sdk-go %s —— aiod v2 API 遥控 CLI\n\n", Version)
	fmt.Fprintln(os.Stderr, "用法: sandbox-sdk-go <命令> [参数...]")
	fmt.Fprintln(os.Stderr, "环境: SANDBOX_BASE（必填）SANDBOX_KEY（可选）")
	fmt.Fprintln(os.Stderr)
	w := tabwriter.NewWriter(os.Stderr, 0, 2, 2, ' ', 0)
	names := make([]string, 0, len(commands))
	byName := map[string]command{}
	for _, c := range commands {
		names = append(names, c.name)
		byName[c.name] = c
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %s\t%s\n", n, byName[n].usage)
	}
	_ = w.Flush()
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	name := args[0]
	for _, c := range commands {
		if c.name == name {
			if err := c.fn(args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, "错误: "+err.Error())
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "未知命令: %s（用 help 查看全部）\n", name)
	os.Exit(2)
}

// ---------------------------------------------------------------- 输出工具

// printJSON 以缩进 JSON 打印任意值。
func printJSON(v any) {
	if v == nil {
		fmt.Println("null")
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(asString(v))
		return
	}
	fmt.Println(string(b))
}

// printData 默认输出：字符串原样打印，其它走 JSON。
func printData(v any) {
	if s, ok := v.(string); ok {
		fmt.Println(strings.TrimRight(s, "\n"))
		return
	}
	printJSON(v)
}

// mustClient 统一处理客户端构造失败。
func mustClient() (*Client, error) {
	return NewClient()
}

// needArgs 校验必填位置参数数量。
func needArgs(pos []string, n int, usage string) error {
	if len(pos) < n {
		return fmt.Errorf("参数不足，用法: %s", usage)
	}
	return nil
}

// envMap 解析 --env=K=V,K2=V2 形式。
func envMap(s string) map[string]any {
	out := map[string]any{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}
