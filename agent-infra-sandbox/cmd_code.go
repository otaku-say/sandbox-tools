package main

// cmd_code.go —— `code` / `jupyter` / `nodejs` / `util` 四个命名空间的入口
//
// 动作清单（《API-REFERENCE.md》§3.5 / §3.6 / §3.15 / §3.16）：
//
//	code    run|info                                    方法：ExecuteCode / GetInfo
//	jupyter run|info|ls|sessions|new|rm|rm-all          方法：ExecuteCode / GetInfo / ListSessions（ls 与 sessions 两个动作名）/ CreateSession / DeleteSession / DeleteSessions
//	nodejs  run|info|ls|new|get|rm|update               方法：ExecuteCode / GetInfo / ListSessions / CreateSession / GetSession / DeleteSession / UpdateSession
//	util    markdown                                    方法：ConvertToMarkdown
//
// 约定：args[0] 为动作名；flags, pos := parseFlags(args[1:])；调用 SDK 后统一 printJSON(resp)。

import (
	"fmt"
	"io"
	"os"
	"strings"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

// ---------- code（2 个方法） ----------

// cmdCodeNS 是 `code` 命名空间入口。
func cmdCodeNS(c *client.Client, args []string) {
	const usageText = `sandbox-sdk-go code <动作> [参数]

  run   <python|javascript> [代码|--code=...|stdin] [--timeout=N] [--cwd=...] [--stateful] [--session-id=...]
  info  查询支持的代码运行时
`
	if len(args) > 0 && args[0] == "code" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])

	// opt 取可选字符串选项（缺省 nil 指针）。
	opt := func(k string) *string {
		if v := fget(flags, k, ""); v != "" {
			return sandboxsdkgo.String(v)
		}
		return nil
	}
	// codeArg 解析要执行的代码：位置参数 idx 槽 > --code > 管道 stdin。
	codeArg := func(idx int, usage string) string {
		if len(pos) > idx {
			return pos[idx]
		}
		if v := fget(flags, "code", ""); v != "" {
			return v
		}
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fatal("读取 stdin: %v", err)
			}
			if text := strings.TrimSpace(string(data)); text != "" {
				return string(data)
			}
		}
		fatal("用法: %s", usage)
		return ""
	}

	switch action {

	case "run": // ExecuteCode
		const u = "sandbox-sdk-go code run <python|javascript> [代码|--code=...|stdin] [--timeout=N] [--cwd=...] [--stateful] [--session-id=...]"
		if len(pos) < 1 {
			fatal("用法: %s", u)
		}
		code := codeArg(1, u)
		lang := sandboxsdkgo.Language(pos[0])
		switch pos[0] {
		case "python", "py":
			lang = sandboxsdkgo.LanguagePython
		case "javascript", "js", "node", "nodejs":
			lang = sandboxsdkgo.LanguageJavascript
		}
		resp, err := c.Code.ExecuteCode(ctx, &sandboxsdkgo.CodeExecuteRequest{
			Language:  lang,
			Code:      code,
			Timeout:   fint(flags, "timeout"),
			Cwd:       opt("cwd"),
			Stateful:  fbool(flags, "stateful"),
			SessionId: opt("session-id"),
		})
		if err != nil {
			fatal("code run: %v", err)
		}
		printJSON(resp)

	case "info": // GetInfo
		resp, err := c.Code.GetInfo(ctx)
		if err != nil {
			fatal("code info: %v", err)
		}
		printJSON(resp)

	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}

// ---------- jupyter（6 个方法） ----------

// cmdJupyter 是 `jupyter` 命名空间入口。
func cmdJupyter(c *client.Client, args []string) {
	const usageText = `sandbox-sdk-go jupyter <动作> [参数]

  run       [代码|--code=...|stdin] [--timeout=N] [--kernel=python3] [--session-id=...] [--cwd=...]
  info      内核信息（默认内核 / 可用内核 / 活动会话数）
  ls        列出活动会话（同 sessions）
  sessions  列出活动会话（同 ls）
  new       [--session-id=...] [--kernel=python3] [--cwd=...]
  rm        <sessionId>
  rm-all    清理全部活动会话
`
	if len(args) > 0 && args[0] == "jupyter" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])

	opt := func(k string) *string {
		if v := fget(flags, k, ""); v != "" {
			return sandboxsdkgo.String(v)
		}
		return nil
	}
	codeArg := func(idx int, usage string) string {
		if len(pos) > idx {
			return pos[idx]
		}
		if v := fget(flags, "code", ""); v != "" {
			return v
		}
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fatal("读取 stdin: %v", err)
			}
			if text := strings.TrimSpace(string(data)); text != "" {
				return string(data)
			}
		}
		fatal("用法: %s", usage)
		return ""
	}
	need := func(n int, usage string) {
		if len(pos) < n {
			fatal("用法: %s", usage)
		}
	}

	switch action {

	case "run": // ExecuteCode
		code := codeArg(0, "sandbox-sdk-go jupyter run [代码|--code=...|stdin] [--timeout=N] [--kernel=python3] [--session-id=...] [--cwd=...]")
		resp, err := c.Jupyter.ExecuteCode(ctx, &sandboxsdkgo.JupyterExecuteRequest{
			Code:       code,
			Timeout:    fint(flags, "timeout"),
			KernelName: opt("kernel"),
			SessionId:  opt("session-id"),
			Cwd:        opt("cwd"),
		})
		if err != nil {
			fatal("jupyter run: %v", err)
		}
		printJSON(resp)

	case "info": // GetInfo
		resp, err := c.Jupyter.GetInfo(ctx)
		if err != nil {
			fatal("jupyter info: %v", err)
		}
		printJSON(resp)

	case "ls", "sessions": // ListSessions（两个动作名等价）
		resp, err := c.Jupyter.ListSessions(ctx)
		if err != nil {
			fatal("jupyter ls: %v", err)
		}
		printJSON(resp)

	case "new": // CreateSession
		resp, err := c.Jupyter.CreateSession(ctx, &sandboxsdkgo.JupyterCreateSessionRequest{
			SessionId:  opt("session-id"),
			KernelName: opt("kernel"),
			Cwd:        opt("cwd"),
		})
		if err != nil {
			fatal("jupyter new: %v", err)
		}
		printJSON(resp)

	case "rm": // DeleteSession
		need(1, "sandbox-sdk-go jupyter rm <sessionId>")
		resp, err := c.Jupyter.DeleteSession(ctx, pos[0])
		if err != nil {
			fatal("jupyter rm: %v", err)
		}
		printJSON(resp)

	case "rm-all": // DeleteSessions
		resp, err := c.Jupyter.DeleteSessions(ctx)
		if err != nil {
			fatal("jupyter rm-all: %v", err)
		}
		printJSON(resp)

	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}

// ---------- nodejs（7 个方法） ----------

// cmdNodejs 是 `nodejs` 命名空间入口。
func cmdNodejs(c *client.Client, args []string) {
	const usageText = `sandbox-sdk-go nodejs <动作> [参数]

  run     [代码|--code=...|stdin] [--timeout=N] [--stdin=文本] [--files=名=内容;名2=内容2] [--stateful] [--session-id=...] [--cwd=...] [--version=node20|node22|node24]
  info    REPL 运行时信息（node/npm 版本、已装包、可用版本）
  ls      列出活动会话
  new     [--session-id=...] [--cwd=...] [--max-idle-time=N]
  get     <sessionId>
  rm      <sessionId>
  update  <sessionId> [--max-idle-time=N] [--cwd=...]
`
	if len(args) > 0 && args[0] == "nodejs" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])

	opt := func(k string) *string {
		if v := fget(flags, k, ""); v != "" {
			return sandboxsdkgo.String(v)
		}
		return nil
	}
	codeArg := func(idx int, usage string) string {
		if len(pos) > idx {
			return pos[idx]
		}
		if v := fget(flags, "code", ""); v != "" {
			return v
		}
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fatal("读取 stdin: %v", err)
			}
			if text := strings.TrimSpace(string(data)); text != "" {
				return string(data)
			}
		}
		fatal("用法: %s", usage)
		return ""
	}
	need := func(n int, usage string) {
		if len(pos) < n {
			fatal("用法: %s", usage)
		}
	}
	// filesMap 解析 --files=名字=内容;名字2=内容2（分号分隔条目，值可含逗号）。
	filesMap := func() map[string]*string {
		v := fget(flags, "files", "")
		if v == "" {
			return nil
		}
		out := map[string]*string{}
		for _, item := range strings.Split(v, ";") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name, content := item, ""
			if i := strings.Index(item, "="); i >= 0 {
				name, content = strings.TrimSpace(item[:i]), item[i+1:]
			}
			if name != "" {
				out[name] = sandboxsdkgo.String(content)
			}
		}
		return out
	}

	switch action {

	case "run": // ExecuteCode
		code := codeArg(0, "sandbox-sdk-go nodejs run [代码|--code=...|stdin] [--timeout=N] [--stdin=文本] [--files=名=内容;...] [--stateful] [--session-id=...] [--cwd=...] [--version=node20|node22|node24]")
		resp, err := c.Nodejs.ExecuteCode(ctx, &sandboxsdkgo.NodeJsExecuteRequest{
			Code:      code,
			Timeout:   fint(flags, "timeout"),
			Stdin:     opt("stdin"),
			Files:     filesMap(),
			Stateful:  fbool(flags, "stateful"),
			SessionId: opt("session-id"),
			Cwd:       opt("cwd"),
			Version:   opt("version"),
		})
		if err != nil {
			fatal("nodejs run: %v", err)
		}
		printJSON(resp)

	case "info": // GetInfo
		resp, err := c.Nodejs.GetInfo(ctx)
		if err != nil {
			fatal("nodejs info: %v", err)
		}
		printJSON(resp)

	case "ls": // ListSessions
		resp, err := c.Nodejs.ListSessions(ctx)
		if err != nil {
			fatal("nodejs ls: %v", err)
		}
		printJSON(resp)

	case "new": // CreateSession
		resp, err := c.Nodejs.CreateSession(ctx, &sandboxsdkgo.NodeJsCreateSessionRequest{
			SessionId:   opt("session-id"),
			Cwd:         opt("cwd"),
			MaxIdleTime: fint(flags, "max-idle-time"),
		})
		if err != nil {
			fatal("nodejs new: %v", err)
		}
		printJSON(resp)

	case "get": // GetSession
		need(1, "sandbox-sdk-go nodejs get <sessionId>")
		resp, err := c.Nodejs.GetSession(ctx, pos[0])
		if err != nil {
			fatal("nodejs get: %v", err)
		}
		printJSON(resp)

	case "rm": // DeleteSession
		need(1, "sandbox-sdk-go nodejs rm <sessionId>")
		resp, err := c.Nodejs.DeleteSession(ctx, pos[0])
		if err != nil {
			fatal("nodejs rm: %v", err)
		}
		printJSON(resp)

	case "update": // UpdateSession
		need(1, "sandbox-sdk-go nodejs update <sessionId> [--max-idle-time=N] [--cwd=...]")
		resp, err := c.Nodejs.UpdateSession(ctx, pos[0], &sandboxsdkgo.NodeJsUpdateSessionRequest{
			MaxIdleTime: fint(flags, "max-idle-time"),
			Cwd:         opt("cwd"),
		})
		if err != nil {
			fatal("nodejs update: %v", err)
		}
		printJSON(resp)

	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}

// ---------- util（1 个方法） ----------

// cmdUtil 是 `util` 命名空间入口。
func cmdUtil(c *client.Client, args []string) {
	const usageText = `sandbox-sdk-go util <动作> [参数]

  markdown  <URI>  将 URI（网页/文件）转换为 Markdown
`
	if len(args) > 0 && args[0] == "util" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	action := args[0]
	_, pos := parseFlags(args[1:])

	switch action {

	case "markdown": // ConvertToMarkdown
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go util markdown <URI>")
		}
		resp, err := c.Util.ConvertToMarkdown(ctx, &sandboxsdkgo.UtilConvertToMarkdownRequest{
			Uri: pos[0],
		})
		if err != nil {
			fatal("util markdown: %v", err)
		}
		printJSON(resp)

	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}
