// cmd_ops.go —— `hooks` / `ctxinfo`（client.Sandbox 共 6 方法）、`proxy`（11 方法）、
// `display`（1 方法）、`auth`（2 方法）的 CLI 实现。
//
//	hooks    ls | add | rm                                  ← ListHooks / RegisterHook / RemoveHook
//	ctxinfo  context | py-packages | node-packages | hooks   ← GetContext / GetPythonPackages / GetNodejsPackages / ListHooks
//	proxy    ls | add | rm | excludes | exclude-add | exclude-rm |
//	         upstream | upstream-set | upstream-rm | health | diagnose
//	display  record                                         ← Record
//	auth     ticket | verify                                ← CreateTicket / Authenticate
//
// 入口签名与 dispatch.go 一致：args[0] 为动作名（namespace 由调度层剥掉）。
package main

import (
	"fmt"
	"os"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

// ---------- hooks（client.Sandbox 生命周期钩子） ----------

// hooksUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func hooksUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go hooks <动作>

  ls  [--event <事件>]                                             列出生命周期钩子（ListHooks）
  add <name> <command> [--event E] [--timeout 秒] [--priority N]   注册钩子（RegisterHook）
  rm  <name>                                                       删除钩子（RemoveHook）
`)
	os.Exit(code)
}

func cmdHooks(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[hooks,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "hooks" {
		args = args[1:]
	}
	if len(args) == 0 {
		hooksUsage(os.Stdout, 0)
	}
	flags, pos := parseFlags(args[1:])
	switch args[0] {
	case "ls":
		printSandboxHooks(c, fget(flags, "event", ""))
	case "add":
		if len(pos) < 2 {
			fatal("用法: sandbox-sdk-go hooks add <name> <command> [--event E] [--timeout 秒] [--priority N]")
		}
		req := &sandboxsdkgo.RegisterHookRequest{Name: pos[0], Command: pos[1]}
		if v := fget(flags, "event", ""); v != "" {
			req.Event = sandboxsdkgo.String(v)
		}
		req.Timeout = ffloat(flags, "timeout")
		req.Priority = fint(flags, "priority")
		resp, err := c.Sandbox.RegisterHook(ctx, req)
		check(err)
		printJSON(resp)
	case "rm":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go hooks rm <name>")
		}
		resp, err := c.Sandbox.RemoveHook(ctx, pos[0])
		check(err)
		printJSON(resp)
	default:
		hooksUsage(os.Stderr, 2)
	}
}

// printSandboxHooks 拉取钩子列表（event 非空时按事件过滤）；ctxinfo hooks 复用。
func printSandboxHooks(c *client.Client, event string) {
	req := &sandboxsdkgo.SandboxListHooksRequest{}
	if event != "" {
		req.Event = sandboxsdkgo.String(event)
	}
	resp, err := c.Sandbox.ListHooks(ctx, req)
	check(err)
	printJSON(resp)
}

// ---------- ctxinfo（client.Sandbox 上下文类） ----------

// ctxinfoUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func ctxinfoUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go ctxinfo <动作>

  context        沙箱环境上下文：系统 / 运行时 / 工具（GetContext）
  py-packages    已安装的 Python 包（GetPythonPackages）
  node-packages  已安装的 Node.js 包（GetNodejsPackages）
  hooks          生命周期钩子（ListHooks；--event <事件> 过滤）
`)
	os.Exit(code)
}

func cmdCtxInfo(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[ctxinfo,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "ctxinfo" {
		args = args[1:]
	}
	if len(args) == 0 {
		ctxinfoUsage(os.Stdout, 0)
	}
	flags, _ := parseFlags(args[1:])
	switch args[0] {
	case "context":
		resp, err := c.Sandbox.GetContext(ctx)
		check(err)
		printJSON(resp)
	case "py-packages":
		resp, err := c.Sandbox.GetPythonPackages(ctx)
		check(err)
		printJSON(resp)
	case "node-packages":
		resp, err := c.Sandbox.GetNodejsPackages(ctx)
		check(err)
		printJSON(resp)
	case "hooks":
		printSandboxHooks(c, fget(flags, "event", ""))
	default:
		ctxinfoUsage(os.Stderr, 2)
	}
}

// ---------- proxy ----------

// proxyUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func proxyUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go proxy <动作>

  ls                                    列出域名→端口映射（ListMappings）
  add  <path> <target>                  新增/更新映射（AddMapping）
  rm   <source>                         删除映射（RemoveMapping）
  excludes                              列出排除项（ListExcludes）
  exclude-add <pattern>                 新增排除项（AddExclude）
  exclude-rm  <pattern>                 删除排除项（RemoveExclude）
  upstream                              查看上游代理（GetUpstream）
  upstream-set <server> [--auth-cmd C]  设置上游代理（SetUpstream）
  upstream-rm                           移除上游，切直连（RemoveUpstream）
  health                                代理健康检查（Health）
  diagnose <url>                        路由诊断（Diagnose）
`)
	os.Exit(code)
}

func cmdProxy(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[proxy,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "proxy" {
		args = args[1:]
	}
	if len(args) == 0 {
		proxyUsage(os.Stdout, 0)
	}
	flags, pos := parseFlags(args[1:])
	switch args[0] {
	case "ls":
		resp, err := c.Proxy.ListMappings(ctx)
		check(err)
		printJSON(resp)
	case "add":
		if len(pos) < 2 {
			fatal("用法: sandbox-sdk-go proxy add <path> <target>")
		}
		resp, err := c.Proxy.AddMapping(ctx, &sandboxsdkgo.ProxyMappingAddRequest{
			Source: pos[0],
			Target: pos[1],
		})
		check(err)
		printJSON(resp)
	case "rm":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go proxy rm <source>")
		}
		resp, err := c.Proxy.RemoveMapping(ctx, pos[0])
		check(err)
		printJSON(resp)
	case "excludes":
		resp, err := c.Proxy.ListExcludes(ctx)
		check(err)
		printJSON(resp)
	case "exclude-add":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go proxy exclude-add <pattern>")
		}
		resp, err := c.Proxy.AddExclude(ctx, &sandboxsdkgo.ProxyBypassRequest{Pattern: pos[0]})
		check(err)
		printJSON(resp)
	case "exclude-rm":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go proxy exclude-rm <pattern>")
		}
		resp, err := c.Proxy.RemoveExclude(ctx, &sandboxsdkgo.ProxyBypassRequest{Pattern: pos[0]})
		check(err)
		printJSON(resp)
	case "upstream":
		resp, err := c.Proxy.GetUpstream(ctx)
		check(err)
		printJSON(resp)
	case "upstream-set":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go proxy upstream-set <server> [--auth-cmd C]")
		}
		req := &sandboxsdkgo.ProxyUpstreamUpdateRequest{Server: pos[0]}
		if v := fget(flags, "auth-cmd", ""); v != "" {
			req.AuthCmd = sandboxsdkgo.String(v)
		}
		resp, err := c.Proxy.SetUpstream(ctx, req)
		check(err)
		printJSON(resp)
	case "upstream-rm":
		resp, err := c.Proxy.RemoveUpstream(ctx)
		check(err)
		printJSON(resp)
	case "health":
		resp, err := c.Proxy.Health(ctx)
		check(err)
		printJSON(resp)
	case "diagnose":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go proxy diagnose <url>")
		}
		resp, err := c.Proxy.Diagnose(ctx, &sandboxsdkgo.ProxyDiagnoseRequest{Url: pos[0]})
		check(err)
		printJSON(resp)
	default:
		proxyUsage(os.Stderr, 2)
	}
}

// ---------- display（Xvfb 录屏） ----------

// displayUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func displayUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go display record <start|stop|status> [选项]

选项:
  --save-path <文件>     输出文件（默认 /tmp/recordings/recording_{timestamp}.mp4）
  --fps <n>              帧率
  --crf <n>              H.264 CRF（0=无损，51=最差）
  --max-duration <秒>    最长录制时长
  --width <像素>         视频宽（缺省自动探测 X11）
  --height <像素>        视频高（缺省自动探测 X11）
`)
	os.Exit(code)
}

func cmdDisplay(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[display,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "display" {
		args = args[1:]
	}
	if len(args) == 0 {
		displayUsage(os.Stdout, 0)
	}
	flags, pos := parseFlags(args[1:])
	switch args[0] {
	case "record":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go display record <start|stop|status> [选项]")
		}
		var action sandboxsdkgo.DisplayRecordRequestAction
		switch pos[0] {
		case "start":
			action = sandboxsdkgo.DisplayRecordRequestActionStart
		case "stop":
			action = sandboxsdkgo.DisplayRecordRequestActionStop
		case "status":
			action = sandboxsdkgo.DisplayRecordRequestActionStatus
		default:
			fatal("display record 的 action 必须是 start|stop|status，收到 %q", pos[0])
		}
		req := &sandboxsdkgo.DisplayRecordRequest{
			Action:      action,
			Fps:         fint(flags, "fps"),
			Crf:         fint(flags, "crf"),
			MaxDuration: ffloat(flags, "max-duration"),
			Width:       fint(flags, "width"),
			Height:      fint(flags, "height"),
		}
		if v := fget(flags, "save-path", ""); v != "" {
			req.SavePath = sandboxsdkgo.String(v)
		}
		resp, err := c.Display.Record(ctx, req)
		check(err)
		printJSON(resp)
	default:
		displayUsage(os.Stderr, 2)
	}
}

// ---------- auth ----------

// authUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func authUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go auth <动作>

  ticket   创建短期认证 ticket（CreateTicket）
  verify   校验请求（Authenticate；nginx auth_request 场景）
`)
	os.Exit(code)
}

func cmdAuth(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[auth,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "auth" {
		args = args[1:]
	}
	if len(args) == 0 {
		authUsage(os.Stdout, 0)
	}
	switch args[0] {
	case "ticket":
		ticket, err := c.Auth.CreateTicket(ctx)
		check(err)
		printJSON(ticket)
	case "verify":
		info, err := c.Auth.Authenticate(ctx)
		check(err)
		printJSON(info)
	default:
		authUsage(os.Stderr, 2)
	}
}
