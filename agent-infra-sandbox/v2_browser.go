// v2_browser.go —— 浏览器·导航面：信息 / 导航 / 截图 / 求值 / 快照 / 点击 / 填表（纯 v2 API）
//
// 路由（见 V2-API.md）：
//
//	GET  /v2/browser/info        —— 浏览器就绪状态 / 版本 / CDP 端点
//	POST /v2/browser/navigate    —— 打开页面并等待 wait_until（url/wait_until/timeout/tab_id）
//	GET  /v2/browser/screenshot  —— 原始 PNG 字节（走 apiRaw；format/full_page/quality）
//	POST /v2/browser/evaluate    —— 页面内求值 JS 表达式（expression/await_promise/tab_id）
//	POST /v2/browser/snapshot    —— 可交互元素快照（interactive_only/tab_id）
//	POST /v2/browser/click       —— 点击元素（selector | ref / tab_id）
//	POST /v2/browser/fill        —— 填写表单（selector | ref / value / tab_id）
//
// 输出约定：每个命令都支持 --json（原样打印 data）；
// 需要指定标签页时加 --tab=<tab_id>（--tab-id= / --tab_id= 亦可）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 子命令

// cmdBrInfo 查询浏览器信息（GET /v2/browser/info）。
// 默认输出平铺的 "key: value"（ready / 版本 / CDP 端点优先展示）；--json 原样打印。
// 用法：br-info [--json]
func cmdBrInfo(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/browser/info", nil, nil)
	if err != nil {
		return fmt.Errorf("查询浏览器信息失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	if m := asMap(data); m != nil {
		brnPrintInfo(m)
		return nil
	}
	printData(data)
	return nil
}

// cmdBrGo 导航到指定 URL（POST /v2/browser/navigate），成功后打印 url 与 title。
// navigate 响应没带 title 时，补一次 evaluate("document.title") 读取。
// 用法：br-go <url> [--wait=load|domcontentloaded|networkidle] [--timeout=秒] [--tab=] [--json]
func cmdBrGo(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-go <url> [--wait=load|domcontentloaded|networkidle] [--timeout=] [--tab=] [--json]"); err != nil {
		return err
	}
	target := pos[0]
	wait := flags["wait"]
	if wait == "" {
		wait = "load"
	}
	timeoutSec := 30
	if v, ok := flags["timeout"]; ok {
		timeoutSec = parseInt(v, 30)
		if timeoutSec <= 0 {
			timeoutSec = 30
		}
	}
	tab := brnTabID(flags)
	body := map[string]any{"url": target, "wait_until": wait, "timeout": timeoutSec}
	if tab != "" {
		body["tab_id"] = tab
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	// 导航最坏情况耗满服务端 timeout，HTTP 侧多给 20s 余量。
	data, err := c.apiT("POST", "/v2/browser/navigate", nil, body, time.Duration(timeoutSec+20)*time.Second)
	if err != nil {
		return fmt.Errorf("导航失败（url=%s wait_until=%s timeout=%ds）: %w", target, wait, timeoutSec, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印
		return nil
	}
	m := asMap(data)
	finalURL := asString(m["url"])
	if finalURL == "" {
		finalURL = target
	}
	title := asString(m["title"])
	if title == "" {
		evalBody := map[string]any{"expression": "document.title"}
		if tab != "" {
			evalBody["tab_id"] = tab
		}
		if td, err2 := c.api("POST", "/v2/browser/evaluate", nil, evalBody); err2 == nil {
			title = brnValueText(td)
		}
	}
	fmt.Printf("url: %s\n", finalURL)
	fmt.Printf("title: %s\n", title)
	brnPrintRest(data, "url", "title")
	return nil
}

// cmdBrEval 在页面里求值（POST /v2/browser/evaluate），默认打印 data.value。
// 用法：br-eval <表达式> [--await] [--tab=] [--json]
func cmdBrEval(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-eval <表达式> [--await] [--tab=] [--json]"); err != nil {
		return err
	}
	body := map[string]any{"expression": pos[0], "await_promise": flagBool(flags, "await")}
	if tab := brnTabID(flags); tab != "" {
		body["tab_id"] = tab
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/evaluate", nil, body)
	if err != nil {
		return fmt.Errorf("求值失败（expression=%q）: %w", pos[0], err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	// 人类可读：优先取 data.value；字符串原样打印，其余 JSON 化。
	if m := asMap(data); m != nil {
		if v, ok := m["value"]; ok {
			if s, ok := v.(string); ok {
				fmt.Println(strings.TrimRight(s, "\n"))
			} else {
				printJSON(v)
			}
			return nil
		}
	}
	printData(data)
	return nil
}

// cmdBrShot 截图（GET /v2/browser/screenshot，返回原始 PNG 字节），写入本地文件。
// 默认 png；--full 抓整页；--quality 仅对 jpeg 有意义（png 时服务端会忽略）。
// 用法：br-shot <输出文件.png> [--full] [--quality=0-100] [--format=png|jpeg] [--json]
func cmdBrShot(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-shot <输出文件.png> [--full] [--quality=0-100] [--json]"); err != nil {
		return err
	}
	out := pos[0]
	format := flags["format"]
	if format == "" {
		format = "png"
	}
	q := map[string]string{"format": format}
	full := flagBool(flags, "full")
	if full {
		q["full_page"] = "true"
	}
	if v, ok := flags["quality"]; ok {
		n := parseInt(v, -1)
		if n < 0 || n > 100 {
			return fmt.Errorf("--quality 需为 0-100 的整数（得到 %q）", v)
		}
		q["quality"] = fmt.Sprintf("%d", n)
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("GET", "/v2/browser/screenshot", q, nil, 180*time.Second)
	if err != nil {
		return fmt.Errorf("截图失败: %w", err)
	}
	// format=png 时校验 PNG 魔术数，避免把错误页当图片写盘。
	if format == "png" && !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		msg := strings.TrimSpace(string(brnHead(raw, 200)))
		var env struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Message != "" {
			msg = env.Message
		}
		return fmt.Errorf("截图响应不是 PNG（%d 字节）: %s", len(raw), msg)
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return fmt.Errorf("写入截图文件失败（%s）: %w", out, err)
	}
	abs := out
	if p, err2 := filepath.Abs(out); err2 == nil {
		abs = p
	}
	if flagBool(flags, "json") {
		printJSON(map[string]any{"file": abs, "bytes": len(raw), "format": format, "full_page": full})
		return nil
	}
	fmt.Printf("已保存截图: %s（%d 字节，format=%s）\n", abs, len(raw), format)
	return nil
}

// cmdBrSnapshot 获取页面快照（POST /v2/browser/snapshot），默认人类可读（树或条目列表）。
// 用法：br-snapshot [--interactive] [--tab=] [--json]
func cmdBrSnapshot(args []string) error {
	_, flags := splitFlags(args)
	body := map[string]any{"interactive_only": flagBool(flags, "interactive")}
	if tab := brnTabID(flags); tab != "" {
		body["tab_id"] = tab
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/snapshot", nil, body)
	if err != nil {
		return fmt.Errorf("获取快照失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	brnPrintSnapshot(data)
	return nil
}

// cmdBrClick 点击元素（POST /v2/browser/click），--selector 与 --ref 二选一。
// 用法：br-click (--selector= | --ref=) [--tab=] [--json]
func cmdBrClick(args []string) error {
	_, flags := splitFlags(args)
	sel, ref := flags["selector"], flags["ref"]
	if sel == "" && ref == "" {
		return fmt.Errorf("缺少 --selector= 或 --ref=，用法: br-click (--selector= | --ref=) [--tab=] [--json]")
	}
	tab := brnTabID(flags)
	body := map[string]any{}
	if sel != "" {
		body["selector"] = sel
	}
	if ref != "" {
		body["ref"] = ref
	}
	if tab != "" {
		body["tab_id"] = tab
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/click", nil, body)
	if err != nil {
		return fmt.Errorf("点击失败（%s）: %w", brnTargetDesc(sel, ref), err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	fmt.Printf("点击成功: %s\n", brnTargetDesc(sel, ref))
	brnPrintReply(data, map[string]string{"selector": sel, "ref": ref, "tab_id": tab})
	return nil
}

// cmdBrFill 填写表单（POST /v2/browser/fill），--selector 与 --ref 二选一，--value 必填。
// 用法：br-fill (--selector= | --ref=) --value=<内容> [--tab=] [--json]
func cmdBrFill(args []string) error {
	_, flags := splitFlags(args)
	sel, ref := flags["selector"], flags["ref"]
	if sel == "" && ref == "" {
		return fmt.Errorf("缺少 --selector= 或 --ref=，用法: br-fill (--selector= | --ref=) --value=<内容> [--tab=] [--json]")
	}
	value, ok := flags["value"]
	if !ok {
		return fmt.Errorf("缺少 --value=<内容>，用法: br-fill (--selector= | --ref=) --value=<内容> [--tab=] [--json]")
	}
	tab := brnTabID(flags)
	body := map[string]any{"value": value}
	if sel != "" {
		body["selector"] = sel
	}
	if ref != "" {
		body["ref"] = ref
	}
	if tab != "" {
		body["tab_id"] = tab
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/fill", nil, body)
	if err != nil {
		return fmt.Errorf("填写失败（%s value=%q）: %w", brnTargetDesc(sel, ref), value, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	fmt.Printf("填写成功: %s value=%q\n", brnTargetDesc(sel, ref), value)
	brnPrintReply(data, map[string]string{"selector": sel, "ref": ref, "value": value, "tab_id": tab})
	return nil
}

// ---------------------------------------------------------------- 内部工具（brn 前缀防撞名）

// brnTabID 依次从 --tab= / --tab-id= / --tab_id= 取标签页 id（都可选）。
func brnTabID(flags map[string]string) string {
	for _, k := range []string{"tab", "tab-id", "tab_id"} {
		if v := flags[k]; v != "" {
			return v
		}
	}
	return ""
}

// brnTargetDesc 描述 click/fill 的目标，用于确认行与错误信息。
func brnTargetDesc(sel, ref string) string {
	switch {
	case sel != "" && ref != "":
		return fmt.Sprintf("selector=%q ref=%q", sel, ref)
	case sel != "":
		return fmt.Sprintf("selector=%q", sel)
	default:
		return fmt.Sprintf("ref=%q", ref)
	}
}

// brnHead 取前 n 字节（不足时全量），用于错误提示里展示响应片段。
func brnHead(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// brnValueText 从 evaluate / navigate 响应里尽力取出"值"的文本。
func brnValueText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m := asMap(v); m != nil {
		for _, k := range []string{"value", "result", "text", "title"} {
			if x, ok := m[k]; ok && x != nil {
				if s, ok := x.(string); ok {
					return s
				}
				return asString(x)
			}
		}
	}
	if v == nil {
		return ""
	}
	return asString(v)
}

// brnInfoPriority 是 br-info 人类可读输出里优先展示的字段（存在才打印）。
var brnInfoPriority = []string{
	"ready", "available", "browser", "browser_version", "version",
	"cdp_url", "cdp_endpoint", "ws_endpoint", "ws_url", "cdp",
}

// brnPrintInfo 平铺打印一个 map：优先键在前，其余按 key 排序。
func brnPrintInfo(m map[string]any) {
	used := map[string]bool{}
	for _, k := range brnInfoPriority {
		if v, ok := m[k]; ok {
			used[k] = true
			for _, ln := range brnFlatten(k, v) {
				fmt.Println(ln)
			}
		}
	}
	for _, k := range brnSortedKeys(m) {
		if used[k] {
			continue
		}
		for _, ln := range brnFlatten(k, m[k]) {
			fmt.Println(ln)
		}
	}
}

// brnPrintRest 打印 data 里除 exclude 之外的字段（最多 20 行）。
func brnPrintRest(data any, exclude ...string) {
	m := asMap(data)
	if len(m) == 0 {
		return
	}
	ex := map[string]bool{}
	for _, k := range exclude {
		ex[k] = true
	}
	rest := map[string]any{}
	for k, v := range m {
		if !ex[k] {
			rest[k] = v
		}
	}
	brnPrintExtras(rest, 20)
}

// brnPrintExtras 把 map 平铺打印（缩进两格），最多 max 行，超出提示用 --json。
func brnPrintExtras(data any, max int) {
	m := asMap(data)
	if len(m) == 0 {
		return
	}
	lines := brnFlattenAll(m)
	for i, ln := range lines {
		if i >= max {
			fmt.Printf("  …（另有 %d 行，--json 查看完整）\n", len(lines)-max)
			break
		}
		fmt.Println("  " + ln)
	}
}

// brnFlattenAll 平铺一个 map（key 排序，点号连接嵌套路径）。
func brnFlattenAll(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for _, k := range brnSortedKeys(m) {
		out = append(out, brnFlatten(k, m[k])...)
	}
	return out
}

// brnFlatten 递归展开任意 JSON 值：
//   - map → "父.子: 值"；空 map → "父: {}"
//   - 全标量数组 → 合并成一行 "[a, b]"；含对象数组 → 按 "[i]" 索引展开
//   - nil → 空值行
func brnFlatten(prefix string, v any) []string {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return []string{prefix + ": {}"}
		}
		out := make([]string, 0, len(t))
		for _, k := range brnSortedKeys(t) {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out = append(out, brnFlatten(p, t[k])...)
		}
		return out
	case []any:
		if len(t) == 0 {
			return []string{prefix + ": []"}
		}
		scalars := true
		for _, e := range t {
			switch e.(type) {
			case map[string]any, []any:
				scalars = false
			}
		}
		if scalars {
			parts := make([]string, 0, len(t))
			for _, e := range t {
				parts = append(parts, brnScalar(e))
			}
			return []string{prefix + ": [" + strings.Join(parts, ", ") + "]"}
		}
		out := make([]string, 0, len(t))
		for i, e := range t {
			out = append(out, brnFlatten(fmt.Sprintf("%s[%d]", prefix, i), e)...)
		}
		return out
	case nil:
		return []string{prefix + ": "}
	default:
		return []string{prefix + ": " + brnScalar(v)}
	}
}

// brnScalar 标量转文本（字符串不加引号，其余交给 asString）。
func brnScalar(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return asString(v)
}

// brnSortedKeys 返回 map 的 key 排序结果。
func brnSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// brnPrintSnapshot 人类可读地打印快照结果：
//   - 字符串 → 原样打印；
//   - 树形快照（实测形如 {"role":"RootWebArea","children":[…]}）→ 缩进树；
//   - 含整段文本字段 → 打印文本；
//   - 含元素列表 → 逐条列出；否则按 key/value 平铺兜底。
func brnPrintSnapshot(data any) {
	if s, ok := data.(string); ok {
		fmt.Println(strings.TrimRight(s, "\n"))
		return
	}
	if l := asList(data); l != nil {
		brnPrintElements(l)
		return
	}
	m := asMap(data)
	if m == nil {
		printData(data)
		return
	}
	// 树形快照：节点自带 role（根通常是 RootWebArea）
	if _, ok := m["role"]; ok {
		budget := 200
		brnPrintTreeNode(m, 0, &budget)
		if budget <= 0 {
			fmt.Println("…（快照较大，已截断；--json 查看完整）")
		}
		return
	}
	// 整段文本（树/文本）形式的快照
	for _, k := range []string{"snapshot", "tree", "text", "yaml", "xml", "content", "markdown"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			fmt.Println(strings.TrimRight(s, "\n"))
			return
		}
	}
	// 条目列表形式的快照
	for _, k := range []string{"elements", "nodes", "items", "interactive", "entries"} {
		if l := asList(m[k]); l != nil && len(l) > 0 {
			brnPrintElements(l)
			return
		}
	}
	brnPrintInfo(m) // 兜底：平铺
}

// brnPrintTreeNode 递归打印树形快照（每层缩进两格）；budget 为剩余可打印行数。
func brnPrintTreeNode(node map[string]any, depth int, budget *int) {
	if *budget <= 0 {
		return
	}
	*budget--
	fmt.Println(strings.Repeat("  ", depth) + brnNodeLine(node))
	for _, ch := range asList(node["children"]) {
		if cm := asMap(ch); cm != nil {
			brnPrintTreeNode(cm, depth+1, budget)
		}
	}
}

// brnNodeLine 渲染单个树节点：role "name" ref=eN [其它标量…]。
func brnNodeLine(node map[string]any) string {
	var b strings.Builder
	role := asString(node["role"])
	if role == "" {
		role = "?"
	}
	b.WriteString(role)
	if name := brnEllipsis(asString(node["name"]), 72); name != "" {
		b.WriteString(fmt.Sprintf(" %q", name))
	}
	if ref := asString(node["ref"]); ref != "" {
		b.WriteString(" ref=" + ref)
	}
	for _, k := range brnSortedKeys(node) {
		switch k {
		case "role", "name", "ref", "children":
			continue
		}
		switch node[k].(type) {
		case map[string]any, []any, nil:
			continue
		}
		s := brnEllipsis(brnScalar(node[k]), 48)
		if strings.ContainsAny(s, " \t") {
			b.WriteString(fmt.Sprintf(" %s=%q", k, s))
		} else {
			b.WriteString(fmt.Sprintf(" %s=%s", k, s))
		}
	}
	return b.String()
}

// brnEllipsis 按 rune 截断过长文本（避免截坏 UTF-8）。
func brnEllipsis(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// brnPrintReply 打印 click/fill 响应的附加字段：
// 过滤掉与服务端回显相同的请求项，最多 12 行。
func brnPrintReply(data any, sent map[string]string) {
	m := asMap(data)
	if len(m) == 0 {
		return
	}
	rest := map[string]any{}
	for k, v := range m {
		if s, ok := sent[k]; ok && s != "" && asString(v) == s {
			continue // 与请求相同的回显，跳过
		}
		rest[k] = v
	}
	brnPrintExtras(rest, 12)
}

// brnPrintElements 逐条打印快照元素：先列关键字段（ref/role/name/tag…），再补其余标量。
func brnPrintElements(list []any) {
	for i, e := range list {
		m := asMap(e)
		if m == nil {
			fmt.Printf("[%d] %s\n", i, brnScalar(e))
			continue
		}
		parts := make([]string, 0, 6)
		shown := map[string]bool{}
		for _, k := range []string{"ref", "role", "tag", "name", "text", "type", "value", "placeholder"} {
			v, ok := m[k]
			if !ok || v == nil {
				continue
			}
			s := brnScalar(v)
			if strings.TrimSpace(s) == "" {
				continue
			}
			s = brnEllipsis(s, 64)
			shown[k] = true
			if k == "ref" || k == "role" || k == "tag" || k == "type" {
				parts = append(parts, fmt.Sprintf("%s=%s", k, s))
			} else {
				parts = append(parts, fmt.Sprintf("%s=%q", k, s))
			}
		}
		fmt.Printf("[%d] %s\n", i, strings.Join(parts, " "))
		// 其余标量作为次行补充（对象/数组跳过，避免刷屏）
		for _, k := range brnSortedKeys(m) {
			if shown[k] {
				continue
			}
			switch m[k].(type) {
			case map[string]any, []any, nil:
				continue
			}
			fmt.Printf("      %s: %s\n", k, brnScalar(m[k]))
		}
	}
}
