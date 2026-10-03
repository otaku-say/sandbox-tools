// v2_pty.go —— 终端面（/v2/pty/*）命令实现（纯 v2 HTTP）
//
// 覆盖命令：pty-new / pty / pty-screen / pty-input / pty-signal / pty-resize /
// pty-ls / pty-rm，全部支持 --json（置位时原样打印服务端 data）。
//
// 实测备注（aio-daemon v0.9.2；原始输出见交付报告）：
//   - exec 的 data 里本条命令输出字段是 `output`（PTY 把 stdout/stderr 合流输出，
//     没有独立 stdout 字段）；另有 `console`（本会话历史命令数组）与 `exit_code`。
//   - 会话第一条命令的 output 可能夹带 shell 重绘产生的 ANSI 转义序列；
//     人类可读模式统一做 ANSI 清洗，--json 保留原样。
//   - --timeout=N（秒）到点返回 status=running（命令仍在跑）；此时再 exec 会被
//     服务端拒绝（Session already has a running command），可用 pty-screen 看进度。
//   - signal 实测会把会话进程终止：POST signal 之后会话直接从列表消失（非"仅中断"）。
//   - 会话列表/详情接口都不回传终端 cols/rows（创建与 PATCH 请求中有该字段），
//     因此 pty-ls 的 SIZE 列当前只能显示 "-"（服务端字段缺失时的降级）。
package main

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// ---------------------------------------------------------------- 私有工具（pty 前缀防撞名）

// ptyJSON 是否设置了 --json。
func ptyJSON(flags map[string]string) bool { return flagBool(flags, "json") }

// ptyCleanANSI 去除终端转义序列与其余 C0 控制符（人类可读输出用；--json 不清洗）。
// 支持 CSI（ESC [ … 0x40-0x7e 终止）、OSC（ESC ] … BEL / ESC \）、
// 带中间字节的序列（如 ESC ( 0）与两字节序列（如 ESC =）；\r\n 归一为 \n。
func ptyCleanANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // 转义序列：整体跳过
			i = ptySkipEscape(s, i)
		case c == '\r': // \r\n → \n；孤立 \r（行内重绘）丢弃
			if i+1 < len(s) && s[i+1] == '\n' {
				b.WriteByte('\n')
				i += 2
			} else {
				i++
			}
		case c < 0x20 && c != '\n' && c != '\t': // 其余控制符（BEL/BS 等）丢弃
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// ptySkipEscape 返回跳过 s[i:]（i 指向 ESC）处一个转义序列后的下标。
func ptySkipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch next := s[i+1]; {
	case next == '[': // CSI：参数/中间字节后跟 0x40-0x7e 的终止符
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case next == ']': // OSC：到 BEL 或 ST(ESC \) 为止
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	case next >= 0x20 && next <= 0x2f: // 带中间字节：ESC ( 0 / ESC # 8 等
		j := i + 2
		for j < len(s) && (s[j] < 0x30 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	default: // 两字节序列（ESC = / ESC > 等）
		return i + 2
	}
}

// ptyExecOutput 取出本条命令的输出文本（ANSI 清洗后）：
// 优先 output（实测字段），回退 stdout（兼容其它版本），都没有则返回空串。
func ptyExecOutput(m map[string]any) string {
	out := asString(m["output"])
	if out == "" {
		out = asString(m["stdout"])
	}
	return ptyCleanANSI(out)
}

// ptyTrimDisplay 清掉输出尾部空白/空行，保证补一个换行后不拖 margin。
func ptyTrimDisplay(s string) string { return strings.TrimRight(s, " \t\r\n") }

// ptyExecTimeout 依据 --timeout=<秒> 放宽该次请求的 HTTP 超时（默认 300s，
// 命令级超时 + 30s 缓冲），避免长命令被本地 HTTP 超时先掐断。
func ptyExecTimeout(flags map[string]string) time.Duration {
	if v := strings.TrimSpace(flags["timeout"]); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return time.Duration(f*float64(time.Second)) + 30*time.Second
		}
	}
	return 300 * time.Second
}

// ptyIntFlag 解析整数 flag；缺省/空串返回 def, ok=false。
func ptyIntFlag(flags map[string]string, key string, def int) (int, bool, error) {
	v := strings.TrimSpace(flags[key])
	if v == "" {
		return def, false, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("--%s 不是合法整数: %q", key, v)
	}
	return n, true, nil
}

// ---------------------------------------------------------------- pty-new

// cmdPtyNew：POST /v2/pty/sessions 创建终端会话；人类可读模式打印会话 id 与 cwd。
func cmdPtyNew(args []string) error {
	usage := "pty-new <会话id> [--cwd=] [--cols=] [--rows=] [--retention=persistent|expiring]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	body := map[string]any{"id": pos[0]}
	if v := strings.TrimSpace(flags["cwd"]); v != "" {
		body["cwd"] = v
	}
	if n, ok, err := ptyIntFlag(flags, "cols", 0); err != nil {
		return err
	} else if ok {
		body["cols"] = n
	}
	if n, ok, err := ptyIntFlag(flags, "rows", 0); err != nil {
		return err
	} else if ok {
		body["rows"] = n
	}
	if v := strings.TrimSpace(flags["retention"]); v != "" {
		body["retention"] = v
	}
	data, err := c.api("POST", "/v2/pty/sessions", nil, body)
	if err != nil {
		return fmt.Errorf("创建 pty 会话 %s 失败: %w", pos[0], err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	id := asString(m["session_id"])
	if id == "" {
		id = pos[0]
	}
	fmt.Printf("已创建 pty 会话: %s", id)
	if wd := asString(m["working_dir"]); wd != "" {
		fmt.Printf("（cwd=%s）", wd)
	}
	fmt.Println()
	return nil
}

// ---------------------------------------------------------------- pty（exec）

// cmdPtyExec：POST /v2/pty/sessions/{id}/exec。
// 同步（默认）打印本次命令输出；--async 只打印提交状态（实测 status=running）。
func cmdPtyExec(args []string) error {
	usage := "pty <会话id> <命令> [--timeout=] [--async]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	body := map[string]any{"command": strings.Join(pos[1:], " ")}
	if v := strings.TrimSpace(flags["timeout"]); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("--timeout 不是合法数字（秒）: %q", v)
		}
		body["timeout"] = f
	}
	async := flagBool(flags, "async")
	if async {
		body["async"] = true
	}
	data, err := c.apiT("POST", "/v2/pty/sessions/"+url.PathEscape(id)+"/exec", nil, body, ptyExecTimeout(flags))
	if err != nil {
		return fmt.Errorf("在 pty 会话 %s 执行命令失败: %w", id, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	status := asString(m["status"])
	if async {
		// --async：服务端只回"已提交"状态（output=null），按约定只打印状态。
		if status == "" {
			status = "running"
		}
		fmt.Printf("status=%s\n", status)
		return nil
	}
	if out := ptyTrimDisplay(ptyExecOutput(m)); out != "" {
		fmt.Println(out)
		return nil
	}
	// 无输出：可能是静默命令，也可能 timeout 到点仍在跑 → 退化为状态行。
	line := "status=" + status
	if m["exit_code"] != nil {
		line += fmt.Sprintf(" exit_code=%d", asInt(m["exit_code"], 0))
	}
	fmt.Println(line)
	return nil
}

// ---------------------------------------------------------------- pty-screen

// cmdPtyScreen：GET /v2/pty/sessions/{id}/screen，打印终端屏幕文本（ANSI 清洗）。
// 实测 screen 是长轮询接口：无新输出时会阻塞数秒再返回空 output；
// 命令运行中常返回 output="" + status=running，此时给出"暂无输出"提示而非误报空屏。
func cmdPtyScreen(args []string) error {
	poss, flags := splitFlags(args)
	if err := needArgs(poss, 1, "pty-screen <会话id>"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := poss[0]
	data, err := c.api("GET", "/v2/pty/sessions/"+url.PathEscape(id)+"/screen", nil, nil)
	if err != nil {
		return fmt.Errorf("读取 pty 会话 %s 屏幕失败: %w", id, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	if out := ptyTrimDisplay(ptyExecOutput(m)); out != "" {
		fmt.Println(out)
		return nil
	}
	if st := asString(m["status"]); st == "running" || st == "pending" {
		if cmd := asString(m["command"]); cmd != "" {
			fmt.Printf("（暂无输出；status=%s，当前命令: %s）\n", st, cmd)
		} else {
			fmt.Printf("（暂无输出；status=%s）\n", st)
		}
		return nil
	}
	fmt.Println("（屏幕为空）")
	return nil
}

// ---------------------------------------------------------------- pty-input

// cmdPtyInput：POST /v2/pty/sessions/{id}/input，把文本（可附带回车）写进终端。
func cmdPtyInput(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, "pty-input <会话id> <文本> [--enter]"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	body := map[string]any{
		"input":       strings.Join(pos[1:], " "),
		"press_enter": flagBool(flags, "enter"),
	}
	data, err := c.api("POST", "/v2/pty/sessions/"+url.PathEscape(id)+"/input", nil, body)
	if err != nil {
		return fmt.Errorf("向 pty 会话 %s 发送输入失败: %w", id, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	if st := asString(asMap(data)["status"]); st != "" {
		fmt.Printf("输入已发送（status=%s）\n", st)
	} else {
		fmt.Println("输入已发送")
	}
	return nil
}

// ---------------------------------------------------------------- pty-signal

// cmdPtySignal：POST /v2/pty/sessions/{id}/signal。
// 信号名取第二个位置参数（如 SIGINT），也可用 --signal=；缺省 SIGINT。
// 实测语义：发信号即终止会话进程（会话随后从列表消失），不是"仅中断当前命令"。
func cmdPtySignal(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "pty-signal <会话id> <信号，如 SIGINT>"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	sig := ""
	if len(pos) > 1 {
		sig = pos[1]
	}
	if sig == "" {
		sig = strings.TrimSpace(flags["signal"])
	}
	if sig == "" {
		sig = "SIGINT"
	}
	data, err := c.api("POST", "/v2/pty/sessions/"+url.PathEscape(id)+"/signal", nil, map[string]any{"signal": sig})
	if err != nil {
		return fmt.Errorf("向 pty 会话 %s 发送信号 %s 失败: %w", id, sig, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	line := fmt.Sprintf("信号 %s 已发送", sig)
	if st := asString(m["status"]); st != "" {
		line += fmt.Sprintf(": status=%s", st)
	}
	if m["exit_code"] != nil {
		line += fmt.Sprintf(" exit_code=%d", asInt(m["exit_code"], 0))
	}
	fmt.Println(line)
	return nil
}

// ---------------------------------------------------------------- pty-resize

// cmdPtyResize：PATCH /v2/pty/sessions/{id}，调整终端 cols/rows（至少给一项）。
func cmdPtyResize(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "pty-resize <会话id> --cols= --rows="); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	body := map[string]any{}
	cols, hasCols, err := ptyIntFlag(flags, "cols", 0)
	if err != nil {
		return err
	}
	rows, hasRows, err := ptyIntFlag(flags, "rows", 0)
	if err != nil {
		return err
	}
	if !hasCols && !hasRows {
		return fmt.Errorf("参数不足，用法: pty-resize <会话id> --cols= --rows=（至少给一项）")
	}
	if hasCols {
		body["cols"] = cols
	}
	if hasRows {
		body["rows"] = rows
	}
	data, err := c.api("PATCH", "/v2/pty/sessions/"+url.PathEscape(id), nil, body)
	if err != nil {
		return fmt.Errorf("调整 pty 会话 %s 尺寸失败: %w", id, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	parts := make([]string, 0, 2)
	if hasCols {
		parts = append(parts, fmt.Sprintf("cols=%d", cols))
	}
	if hasRows {
		parts = append(parts, fmt.Sprintf("rows=%d", rows))
	}
	line := "终端尺寸已调整: " + strings.Join(parts, " ")
	if st := asString(asMap(data)["status"]); st != "" {
		line += fmt.Sprintf("（status=%s）", st)
	}
	fmt.Println(line)
	return nil
}

// ---------------------------------------------------------------- pty-ls

// cmdPtyLs：GET /v2/pty/sessions，表格打印 ID / 状态 / 尺寸 / CWD。
// 实测响应不含 cols/rows（响应模型未暴露终端尺寸），SIZE 列在字段缺失时显示 "-"。
func cmdPtyLs(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/pty/sessions", nil, nil)
	if err != nil {
		return fmt.Errorf("列出 pty 会话失败: %w", err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	sess := asMap(m["sessions"])
	if len(sess) == 0 {
		fmt.Println("（无会话）")
	} else {
		ids := make([]string, 0, len(sess))
		for id := range sess {
			ids = append(ids, id)
		}
		sort.Strings(ids) // map 遍历无序，排序保证输出稳定
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tSIZE\tCWD")
		for _, id := range ids {
			sm := asMap(sess[id])
			size := "-" // 服务端当前不回传尺寸；如未来提供则自动显示
			if cols, rows := asInt(sm["cols"], 0), asInt(sm["rows"], 0); cols > 0 || rows > 0 {
				size = fmt.Sprintf("%dx%d", cols, rows)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, asString(sm["status"]), size, asString(sm["working_dir"]))
		}
		w.Flush()
	}
	if st := asMap(m["stats"]); st != nil {
		fmt.Printf("共 %s 个会话（上限 %s）\n", asString(st["total_sessions"]), asString(st["max_sessions"]))
	}
	return nil
}

// ---------------------------------------------------------------- pty-rm

// cmdPtyRm：DELETE /v2/pty/sessions/{id}。
func cmdPtyRm(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "pty-rm <会话id>"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	data, err := c.api("DELETE", "/v2/pty/sessions/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return fmt.Errorf("删除 pty 会话 %s 失败: %w", id, err)
	}
	if ptyJSON(flags) {
		printJSON(data)
		return nil
	}
	rid := asString(asMap(data)["session_id"])
	if rid == "" {
		rid = id
	}
	fmt.Printf("已删除 pty 会话: %s\n", rid)
	return nil
}
