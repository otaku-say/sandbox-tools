// v2_commands.go —— 命令面 v2 实现：exec / async / log / kill / stdin / sess-*
//
// 设计要点（与 SPEC.md 对齐）：
//   - 只用标准库，全部走 client.go 的 c.api / c.apiT，不直接碰 net/http；
//   - 参数一律 --key=value / --flag（splitFlags 已实现）；
//   - 每个命令都支持 --json：置位时原样打印服务端 data，否则打印人类可读结果；
//   - 错误信息带"哪一步 + 服务端 message"，用 %w 包装。
//
// 实测语义（aio-daemon 镜像 v2，2026-10-03 沙箱内实测，两种响应形状都要兼容）：
//   - POST /v2/commands（同步）返回**扁平**字段：
//     data.command_id / status / exit_code / stdout / stderr / output；
//   - GET /v2/commands/{id} 返回 data.stdout / stderr + data.command.{command_id,status,exit_code}
//     嵌套结构；stdout/stderr 是**从 offset 起的增量**（offset 缺省 0），
//     data.offset / data.stderr_offset 是累计端点偏移；
//   - kill / stdin 成功时 data 为空对象 {}；kill 后命令为 completed + exit_code=-1；
//   - 命令会话对象字段为 session_id / working_dir（不是 id / cwd）。
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// ---------------------------------------------------------------- 内部工具（cmdv2 前缀防与其它代理撞名）

// cmdv2Field 取字段：优先 data.command.<key>（GET 形状），退回 data.<key>（POST 形状）。
func cmdv2Field(data map[string]any, key string) any {
	if cmd := asMap(data["command"]); cmd != nil {
		if v, ok := cmd[key]; ok {
			return v
		}
	}
	return data[key]
}

// cmdv2ID 提取 command_id（兼容 POST 扁平 / GET 嵌套两种形状）。
func cmdv2ID(data map[string]any) string {
	return asString(cmdv2Field(data, "command_id"))
}

// cmdv2Status 提取命令状态文本。
func cmdv2Status(data map[string]any) string {
	return asString(cmdv2Field(data, "status"))
}

// cmdv2ExitCode 提取退出码（null/缺失视为 0）。
func cmdv2ExitCode(data map[string]any) int {
	return asInt(cmdv2Field(data, "exit_code"), 0)
}

// cmdv2Float 读取浮点 flag（缺省/空串返回 0）。
func cmdv2Float(flags map[string]string, key string) (float64, error) {
	s := strings.TrimSpace(flags[key])
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("--%s 不是合法数字: %q", key, s)
	}
	return f, nil
}

// cmdv2Dur 解析时长（支持 500ms / 2s / 纯数字=毫秒），空串返回 def。
func cmdv2Dur(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("时长必须为正: %q", s)
		}
		return d, nil
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond, nil
	}
	return 0, fmt.Errorf("不是合法时长: %q（例：500ms、2s）", s)
}

// cmdv2RunBody 按 v2 字段名组装 POST /v2/commands 请求体；未给出的字段不下发。
func cmdv2RunBody(command, mode string, flags map[string]string) (map[string]any, error) {
	body := map[string]any{"command": command}
	if mode != "" {
		body["mode"] = mode
	}
	if v := strings.TrimSpace(flags["cwd"]); v != "" {
		body["cwd"] = v
	}
	if v := strings.TrimSpace(flags["env"]); v != "" {
		body["env"] = envMap(v)
	}
	if f, err := cmdv2Float(flags, "timeout"); err != nil {
		return nil, err
	} else if f > 0 {
		body["timeout"] = f
	}
	if f, err := cmdv2Float(flags, "hard-timeout"); err != nil {
		return nil, err
	} else if f > 0 {
		body["hard_timeout"] = f
	}
	for _, k := range []string{"shell", "user", "session"} {
		if v := strings.TrimSpace(flags[k]); v != "" {
			body[k] = v
		}
	}
	if v, ok := flags["max-output"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("--max-output 不是合法整数: %q", v)
		}
		body["max_output_length"] = n
	}
	return body, nil
}

// cmdv2HTTPTimeout 依据 --timeout/--hard-timeout 放宽 HTTP 超时（默认 300s，
// 避免远端命令还没跑完而客户端先断）。
func cmdv2HTTPTimeout(flags map[string]string) time.Duration {
	to := 300 * time.Second
	for _, k := range []string{"timeout", "hard-timeout"} {
		if f, err := cmdv2Float(flags, k); err == nil && f > 0 {
			if d := time.Duration(f*float64(time.Second)) + 30*time.Second; d > to {
				to = d
			}
		}
	}
	return to
}

// cmdv2Get 拉取 GET /v2/commands/{id}，返回 data 对象。
func cmdv2Get(c *Client, id string, query map[string]string, timeout time.Duration) (map[string]any, error) {
	v, err := c.apiT("GET", "/v2/commands/"+url.PathEscape(id), query, nil, timeout)
	if err != nil {
		return nil, err
	}
	m := asMap(v)
	if m == nil {
		return nil, fmt.Errorf("响应 data 不是对象")
	}
	return m, nil
}

// cmdv2PrintOut 分别把 stdout / stderr 输出写到对应通道（保持原始字节）。
func cmdv2PrintOut(stdout, stderr string) {
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
}

// cmdv2Terminal 判断命令是否到终态（只有 running/pending 视为未结束）。
func cmdv2Terminal(status string) bool {
	switch status {
	case "", "running", "pending":
		return false
	}
	return true
}

// cmdv2Emit 处理一次 GET 响应：打印增量、推进 offset、返回是否终态与退出码。
// 服务端按 offset 返回增量，所以 so/se 直接按本次收到的字节数前进即可。
func cmdv2Emit(data map[string]any, so, se *int, jsonOut bool) (bool, int) {
	stdout := asString(data["stdout"])
	stderr := asString(data["stderr"])
	if jsonOut {
		printJSON(data)
	} else {
		cmdv2PrintOut(stdout, stderr)
	}
	*so += len(stdout)
	if v := asInt(data["offset"], -1); v > *so {
		*so = v // 服务端返回的累计端点偏移更权威
	}
	*se += len(stderr)
	if v := asInt(data["stderr_offset"], -1); v > *se {
		*se = v
	}
	if asMap(data["command"]) == nil && asString(data["status"]) == "" {
		return true, 0 // 响应里没有命令信息（异常形状）：无法继续跟踪，收尾
	}
	return cmdv2Terminal(cmdv2Status(data)), cmdv2ExitCode(data)
}

// ---------------------------------------------------------------- exec

func cmdExec(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	jsonOut := flagBool(flags, "json")

	// 模式一：exec --id=<command_id> —— 查询既有命令，按 offset 增量打印。
	if id := strings.TrimSpace(flags["id"]); id != "" {
		query := map[string]string{}
		if v, ok := flags["offset"]; ok {
			query["offset"] = v
		}
		if v, ok := flags["stderr-offset"]; ok {
			query["stderr_offset"] = v
		}
		if flagBool(flags, "wait") {
			query["wait"] = "true"
		}
		waitTO := 300 * time.Second
		if v := strings.TrimSpace(flags["wait-timeout"]); v != "" {
			query["wait_timeout"] = v
			// wait_timeout 单位以服务端为准（实测为秒）；按秒上限放宽 HTTP 超时即可。
			if f, perr := strconv.ParseFloat(v, 64); perr == nil && f > 0 {
				if d := time.Duration(f*float64(time.Second)) + 30*time.Second; d > waitTO {
					waitTO = d
				}
			}
		}
		data, err := cmdv2Get(c, id, query, waitTO)
		if err != nil {
			return fmt.Errorf("查询命令 %s 失败: %w", id, err)
		}
		if jsonOut {
			printJSON(data)
			return nil
		}
		cmdv2PrintOut(asString(data["stdout"]), asString(data["stderr"]))
		return nil
	}

	// 模式二：同步执行新命令（mode 省略 = 服务端默认同步）。
	if len(pos) == 0 {
		return fmt.Errorf("参数不足，用法: exec <命令> [--cwd=] [--env=] [--timeout=] [--shell=] [--user=] [--max-output=] [--session=] | exec --id=<command_id> [--offset=]")
	}
	body, err := cmdv2RunBody(strings.Join(pos, " "), "", flags)
	if err != nil {
		return err
	}
	data, err := c.apiT("POST", "/v2/commands", nil, body, cmdv2HTTPTimeout(flags))
	if err != nil {
		return fmt.Errorf("执行命令失败: %w", err)
	}
	if jsonOut {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	cmdv2PrintOut(asString(m["stdout"]), asString(m["stderr"]))
	return nil
}

// ---------------------------------------------------------------- async

func cmdAsync(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "async <命令> [--cwd=] [--env=] [--user=]"); err != nil {
		return err
	}
	body, err := cmdv2RunBody(strings.Join(pos, " "), "async", flags)
	if err != nil {
		return err
	}
	data, err := c.apiT("POST", "/v2/commands", nil, body, cmdv2HTTPTimeout(flags))
	if err != nil {
		return fmt.Errorf("异步提交命令失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	id := cmdv2ID(asMap(data))
	if id == "" {
		return fmt.Errorf("服务端未返回 command_id: %s", asString(data))
	}
	fmt.Println(id) // 一行，便于脚本取用
	return nil
}

// ---------------------------------------------------------------- log

func cmdLog(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "log <command_id> [--follow] [--interval=500ms] [--timeout=]"); err != nil {
		return err
	}
	id := pos[0]
	follow := flagBool(flags, "follow")
	jsonOut := flagBool(flags, "json")
	interval, err := cmdv2Dur(flags["interval"], 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("--interval: %w", err)
	}
	limit, err := cmdv2Float(flags, "timeout") // 秒；0=不限
	if err != nil {
		return err
	}
	var deadline time.Time
	if limit > 0 {
		deadline = time.Now().Add(time.Duration(limit * float64(time.Second)))
	}

	so, se := 0, 0
	exitCode := 0
	for {
		query := map[string]string{"offset": strconv.Itoa(so), "stderr_offset": strconv.Itoa(se)}
		data, err := cmdv2Get(c, id, query, 60*time.Second)
		if err != nil {
			return fmt.Errorf("读取命令 %s 输出失败: %w", id, err)
		}
		terminal, code := cmdv2Emit(data, &so, &se, jsonOut)
		exitCode = code
		if terminal || !follow {
			break
		}
		if !deadline.IsZero() {
			remain := time.Until(deadline)
			if remain <= 0 {
				fmt.Fprintf(os.Stderr, "log: 已达 --timeout=%s，停止跟踪（命令仍未结束）\n", strings.TrimSpace(flags["timeout"]))
				break
			}
			if remain < interval {
				time.Sleep(remain)
			} else {
				time.Sleep(interval)
			}
		} else {
			time.Sleep(interval)
		}
	}
	if exitCode != 0 {
		// 与远端命令相同的退出码（负数按 Unix 约定截断为 8bit）。
		os.Exit(exitCode)
	}
	return nil
}

// ---------------------------------------------------------------- kill

func cmdKill(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "kill <command_id> [--signal=SIGKILL]"); err != nil {
		return err
	}
	id := pos[0]
	signal := strings.TrimSpace(flags["signal"])
	if signal == "" {
		signal = "SIGKILL"
	}
	data, err := c.api("POST", "/v2/commands/"+url.PathEscape(id)+"/kill", nil, map[string]any{"signal": signal})
	if err != nil {
		return fmt.Errorf("终止命令 %s 失败: %w", id, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 实测成功时 data={}
		return nil
	}
	fmt.Printf("已发送 %s（命令 %s）\n", signal, id)
	return nil
}

// ---------------------------------------------------------------- stdin

func cmdStdin(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 2, "stdin <command_id> <文本> [--enter]"); err != nil {
		return err
	}
	id := pos[0]
	input := strings.Join(pos[1:], " ")
	if flagBool(flags, "enter") {
		input += "\n" // --enter：补一个换行，便于喂给行缓冲的 read
	}
	data, err := c.api("POST", "/v2/commands/"+url.PathEscape(id)+"/stdin", nil, map[string]any{"input": input})
	if err != nil {
		return fmt.Errorf("向命令 %s 写入 stdin 失败: %w", id, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 实测成功时 data={}
		return nil
	}
	fmt.Printf("已向命令 %s 写入 stdin（%d 字节）\n", id, len(input))
	return nil
}

// ---------------------------------------------------------------- 命令会话

func cmdSessNew(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "sess-new <session_id> [--cwd=] [--env=] [--user=]"); err != nil {
		return err
	}
	body := map[string]any{"id": pos[0]}
	if v := strings.TrimSpace(flags["cwd"]); v != "" {
		body["cwd"] = v
	}
	if v := strings.TrimSpace(flags["env"]); v != "" {
		body["env"] = envMap(v)
	}
	if v := strings.TrimSpace(flags["user"]); v != "" {
		body["user"] = v
	}
	data, err := c.api("POST", "/v2/commands/sessions", nil, body)
	if err != nil {
		return fmt.Errorf("创建命令会话 %s 失败: %w", pos[0], err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	if m == nil {
		printData(data)
		return nil
	}
	if s := asMap(m["session"]); s != nil {
		m = s
	}
	fmt.Printf("会话已创建：id=%s", asString(m["session_id"]))
	if wd := asString(m["working_dir"]); wd != "" {
		fmt.Printf(" cwd=%s", wd)
	}
	if user := asString(m["user"]); user != "" {
		fmt.Printf(" user=%s", user)
	}
	fmt.Println()
	return nil
}

func cmdSess(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 2, "sess <session_id> <命令> [--timeout=] [--max-output=]"); err != nil {
		return err
	}
	sid := pos[0]
	body, err := cmdv2RunBody(strings.Join(pos[1:], " "), "", flags)
	if err != nil {
		return err
	}
	body["session"] = sid // 明确指定会话（覆盖任何 --session=）
	data, err := c.apiT("POST", "/v2/commands", nil, body, cmdv2HTTPTimeout(flags))
	if err != nil {
		return fmt.Errorf("在会话 %s 中执行命令失败: %w", sid, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	cmdv2PrintOut(asString(m["stdout"]), asString(m["stderr"]))
	return nil
}

func cmdSessLs(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/commands/sessions", nil, nil)
	if err != nil {
		return fmt.Errorf("列出命令会话失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	items := asList(data) // 实测 data 直接就是列表
	if items == nil {
		if m := asMap(data); m != nil {
			items = asList(m["sessions"])
		}
	}
	if items == nil {
		printData(data)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("（无命令会话）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tCWD\tCOMMANDS")
	for _, it := range items {
		m := asMap(it)
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			asString(m["session_id"]), asString(m["status"]), asString(m["working_dir"]), asInt(m["command_count"], 0))
	}
	_ = w.Flush()
	return nil
}

func cmdSessRm(args []string) error {
	pos, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "sess-rm <session_id>"); err != nil {
		return err
	}
	id := pos[0]
	data, err := c.api("DELETE", "/v2/commands/sessions/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return fmt.Errorf("删除命令会话 %s 失败: %w", id, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 实测成功时 data={}
		return nil
	}
	fmt.Printf("已删除会话 %s\n", id)
	return nil
}
