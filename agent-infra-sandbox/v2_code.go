// v2_code.go —— code 面命令：执行代码、后端信息、代码会话管理（纯 v2 API）
//
// 路由（见 V2-API.md）：
//
//	POST   /v2/code/execute        —— 执行代码（按 language 分发）
//	GET    /v2/code/info           —— 支持的语言与后端信息
//	GET    /v2/code/sessions       —— 列出代码会话
//	POST   /v2/code/sessions       —— 创建代码会话
//	DELETE /v2/code/sessions/{id}  —— 删除代码会话
//
// 实测要点：
//   - /v2/code/execute 在 status=error 时信封 success=false（data 仍是完整结果），
//     用 api() 会被当失败吞掉 data，所以这里走 apiRaw 自解析；
//   - 会话状态（变量、导入）通过 session_id 在多次调用间保持；
//   - 默认语言 python；--lang=javascript 执行 JS（nodejs 是别名，服务端会归一化）。
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cmdCode 执行一段代码。
// 用法：code <源码> [--lang=python|javascript] [--session=] [--timeout=] [--json]
func cmdCode(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "code <源码> [--lang=python|javascript] [--session=] [--timeout=] [--json]"); err != nil {
		return err
	}
	lang := flags["lang"]
	if lang == "" {
		lang = "python"
	}
	body := map[string]any{"language": lang, "code": pos[0]}
	if sid := flags["session"]; sid != "" {
		body["session_id"] = sid
	}
	httpTimeout := 300 * time.Second
	if t := flags["timeout"]; t != "" {
		secs, err := codeSeconds(t)
		if err != nil {
			return err
		}
		body["timeout"] = secs
		if secs > 0 {
			httpTimeout = time.Duration(secs+60) * time.Second // HTTP 超时留余量
		}
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	// 注意：status=error 时信封 success=false，api() 会吞掉 data → 用 apiRaw 自解析。
	raw, err := c.apiRaw("POST", "/v2/code/execute", nil, body, httpTimeout)
	if err != nil {
		return fmt.Errorf("执行代码失败: %w", err)
	}
	var env struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("执行代码响应解析失败: %s", firstLine(raw))
	}
	if env.Data == nil {
		msg := env.Message
		if msg == "" {
			msg = firstLine(raw)
		}
		return fmt.Errorf("执行代码失败: %s", msg)
	}
	if flagBool(flags, "json") {
		printJSON(env.Data)
	} else {
		codePrintExec(env.Data)
	}
	// 失败状态（status=error）→ 结果已打印，这里返回错误让退出码非 0。
	if codeStatus(env.Data) == "error" {
		if sum := codeErrorSummary(env.Data); sum != "" {
			return fmt.Errorf("代码执行失败（status=error）: %s", sum)
		}
		return fmt.Errorf("代码执行失败（status=error）")
	}
	return nil
}

// cmdCodeInfo 查询 code 后端支持的语言与运行环境。
// 用法：code-info [--json]
func cmdCodeInfo(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/code/info", nil, nil)
	if err != nil {
		return fmt.Errorf("查询 code 后端信息失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	codePrintKV(data)
	return nil
}

// cmdCodeSessNew 创建代码会话。language 缺省由服务端决定（实测默认 python）。
// 用法：code-sess-new [--lang=python|javascript] [--json]
func cmdCodeSessNew(args []string) error {
	_, flags := splitFlags(args)
	body := map[string]any{}
	if lang := flags["lang"]; lang != "" {
		body["language"] = lang
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/code/sessions", nil, body)
	if err != nil {
		return fmt.Errorf("创建代码会话失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	if m := asMap(data); m != nil {
		if sid := codeSessionID(m); sid != "" {
			fmt.Println("session_id: " + sid)
		}
		codePrintKV(m)
		return nil
	}
	printData(data)
	return nil
}

// cmdCodeSessLs 列出全部代码会话（实测 data.sessions 是以 id 为键的对象）。
// 用法：code-sess-ls [--json]
func cmdCodeSessLs(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/code/sessions", nil, nil)
	if err != nil {
		return fmt.Errorf("列出代码会话失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	codePrintSessions(data)
	return nil
}

// cmdCodeSessRm 删除代码会话。实测：会话不存在时服务端仍返回 200 + deleted=false。
// 用法：code-sess-rm <session_id> [--json]
func cmdCodeSessRm(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "code-sess-rm <session_id> [--json]"); err != nil {
		return err
	}
	sid := pos[0]
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("DELETE", "/v2/code/sessions/"+url.PathEscape(sid), nil, nil)
	if err != nil {
		return fmt.Errorf("删除代码会话 %s 失败: %w", sid, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	if m := asMap(data); m != nil {
		if v, ok := m["deleted"]; ok && !codeBool(v) {
			fmt.Printf("会话不存在（deleted=false）: %s\n", sid)
			return nil
		}
	}
	fmt.Printf("已删除代码会话: %s\n", sid)
	return nil
}

// ---------------------------------------------------------------- 内部工具（code 前缀防撞名）

// codeSeconds 解析 --timeout：纯数字按秒，其余按 Go duration（如 500ms / 2m）。
func codeSeconds(s string) (float64, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return float64(n), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--timeout=%s 无法解析（用秒数或 500ms/2m 形式）: %w", s, err)
	}
	return d.Seconds(), nil
}

// codeBool 宽松布尔判定（JSON true / "true" / 1 都算真）。
func codeBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	case float64:
		return t != 0
	}
	return false
}

// codeStatus 取响应中的 status（缺省视为 ok），统一小写。
func codeStatus(data any) string {
	m := asMap(data)
	if m == nil {
		return "ok"
	}
	s := asString(m["status"])
	if s == "" {
		s = asString(m["result"]) // 容错：部分后端用 result 表示执行状态
	}
	if s == "" {
		return "ok"
	}
	return strings.ToLower(s)
}

// codeFieldString 取 map 字段并转字符串（非 map 返回空串）。
func codeFieldString(data any, key string) string {
	if m := asMap(data); m != nil {
		return asString(m[key])
	}
	return ""
}

// codeOutputs 取 data.outputs 列表。
func codeOutputs(m map[string]any) []any {
	return asList(m["outputs"])
}

// codeResultText 从 outputs 里取 execute_result 的 text/plain（表达式结果，如 x*2 → "82"）。
func codeResultText(m map[string]any) string {
	for _, o := range codeOutputs(m) {
		om := asMap(o)
		if asString(om["output_type"]) != "execute_result" {
			continue
		}
		if s := asString(asMap(om["data"])["text/plain"]); s != "" {
			return s
		}
	}
	return ""
}

// codeErrorDetail 提取完整错误明细：优先 stderr，其次 error output 的 traceback。
func codeErrorDetail(m map[string]any) string {
	if s := asString(m["stderr"]); s != "" {
		return s
	}
	for _, o := range codeOutputs(m) {
		om := asMap(o)
		if asString(om["output_type"]) != "error" {
			continue
		}
		if tb := asList(om["traceback"]); len(tb) > 0 {
			lines := make([]string, 0, len(tb))
			for _, l := range tb {
				lines = append(lines, asString(l))
			}
			return strings.Join(lines, "\n")
		}
		ename, evalue := asString(om["ename"]), asString(om["evalue"])
		if ename != "" {
			return strings.TrimSpace(ename + ": " + evalue)
		}
	}
	return ""
}

// codeErrorSummary 一行错误摘要（优先 ename: evalue，其次 traceback 末行）。
func codeErrorSummary(m map[string]any) string {
	for _, o := range codeOutputs(m) {
		om := asMap(o)
		if asString(om["output_type"]) != "error" {
			continue
		}
		ename, evalue := asString(om["ename"]), asString(om["evalue"])
		if ename != "" {
			return strings.TrimSpace(ename + ": " + evalue)
		}
		if tb := asList(om["traceback"]); len(tb) > 0 {
			return asString(tb[len(tb)-1])
		}
	}
	if s := asString(m["stderr"]); s != "" {
		return firstLine([]byte(s))
	}
	return ""
}

// codePrintExec 人类可读的执行结果：stdout（含表达式结果）、stderr、traceback、状态摘要行。
func codePrintExec(data any) {
	m := asMap(data)
	if m == nil {
		printData(data)
		return
	}
	if stdout := codeFieldString(data, "stdout"); stdout != "" {
		fmt.Print(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			fmt.Println()
		}
	} else if text := codeResultText(m); text != "" {
		fmt.Println(text)
	}
	if stderr := codeFieldString(data, "stderr"); stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
		if !strings.HasSuffix(stderr, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}
	if codeStatus(data) == "error" {
		if detail := codeErrorDetail(m); detail != "" {
			fmt.Fprintln(os.Stderr, detail)
		}
	}
	parts := []string{"status=" + codeStatus(data)}
	if v, ok := m["exit_code"]; ok && v != nil {
		parts = append(parts, "exit_code="+asString(v))
	}
	if sid := asString(m["session_id"]); sid != "" {
		parts = append(parts, "session_id="+sid)
	}
	fmt.Println(strings.Join(parts, " "))
}

// codePrintKV 按 key 排序打印 map 的键值（标量直出，列表/对象压成一行）。
func codePrintKV(data any) {
	m := asMap(data)
	if m == nil {
		printData(data)
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s: %s\n", k, codeValueString(m[k]))
	}
}

// codeValueString 把任意 JSON 值压成一行可读文本。
func codeValueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64, bool:
		return asString(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, codeValueString(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return asString(t)
		}
		return string(b)
	}
}

// codeSessionID 从会话对象里提取 id（兼容 session_id / id 两种字段名）。
func codeSessionID(m map[string]any) string {
	for _, k := range []string{"session_id", "id"} {
		if s := asString(m[k]); s != "" {
			return s
		}
	}
	return ""
}

// codePrintSessions 打印会话列表。
// 兼容三种形态：{sessions:{id:obj}}（本部署实测）、{sessions:[...]}、裸数组。
func codePrintSessions(data any) {
	type row struct{ id, lang, state string }
	var rows []row
	addObj := func(m map[string]any) {
		rows = append(rows, row{codeSessionID(m), asString(m["language"]), asString(m["state"])})
	}
	switch {
	case asList(data) != nil:
		for _, item := range asList(data) {
			if m := asMap(item); m != nil {
				addObj(m)
			} else {
				rows = append(rows, row{codeValueString(item), "", ""})
			}
		}
	default:
		m := asMap(data)
		if m == nil {
			printData(data)
			return
		}
		if sm := asMap(m["sessions"]); sm != nil {
			ids := make([]string, 0, len(sm))
			for id := range sm {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				s := asMap(sm[id])
				rows = append(rows, row{id, asString(s["language"]), asString(s["state"])})
			}
		} else if sl := asList(m["sessions"]); sl != nil {
			for _, item := range sl {
				if mm := asMap(item); mm != nil {
					addObj(mm)
				}
			}
		} else {
			printData(data)
			return
		}
	}
	if len(rows) == 0 {
		fmt.Println("(无会话)")
		return
	}
	for _, r := range rows {
		fmt.Printf("%s\t%s\t%s\n", r.id, r.lang, r.state)
	}
}
