// v2_computer.go —— computer-use（桌面）面 v2 实现：
// cmp-info / cmp-shot / cmp-cursor / cmp-clipboard / cmp-windows /
// cmp-a11y / cmp-a11y-nodes / cmp-act / cmp-act-batch / cmp-record
//
// 路由（与沙箱内 /v2/openapi.json、V2-API.md 逐条对齐）：
//
//	GET  /v2/computer/info                      → DisplayInfoEnvelope
//	GET  /v2/computer/screenshot                → 原始 image/png（必须走 apiRaw）
//	GET  /v2/computer/cursor|clipboard|windows  → 普通信封
//	GET  /v2/computer/accessibility             query: scope max_depth max_nodes role name
//	GET  /v2/computer/accessibility/nodes             match states include_offscreen timeout_ms
//	                                                  （nodes 另有 limit、node_id）
//	POST /v2/computer/actions                   body=自由结构 DisplayAction；query include_screenshot
//	POST /v2/computer/actions/batch             body={actions:[...], include_screenshot:bool}
//	POST /v2/computer/record                    body={action crf fps height max_duration save_path width}
//
// 实测语义（2026-10-03，aio-daemon 与 aio-computer 两种镜像均验证）：
//   - aio-daemon 无 computer-use worker：以上路由统一 503（async api 会带出
//     "HTTP 503: ..."），CLI 报清晰错误并以非 0 退出；aio-computer（XFCE+worker）
//     上全部可用，截图 / 点击 / 录制 / 无障碍树均为真实数据。
//   - v2 的 DisplayAction 是 action_type 大写枚举的判别联合（CLICK/MOVE_TO/TYPING…），
//     v1 风格的 {"action":"click","x":…,"y":…} 会被服务端 422（missing field action_type）。
//     因此 cmp-act / cmp-act-batch 采用"加法式"归一化：保留用户全部原字段，仅在缺少
//     action_type 且 "action" 可识别时【追加】派生的 action_type（坐标类动作再补
//     coordinate→x/y）；已经带 action_type 的 body 原样透传、绝不改写。这样文档示例
//     在 aio-computer 真机上可直接运行，同时保持"自由结构直发"的透传语义。
//   - include_screenshot=true 时：单动作的截图在**信封顶层** `screenshot`（base64 PNG，
//     与 data 同级）——冻结的 client.go 只解析信封里的 data，因此该截图不会经 CLI 输出；
//     批量的 screenshot 在 data 内可正常显示（人类可读模式会省略超长 base64 原文，--json 可看全）。
//
// 参数一律 --key=value / --flag；每个命令都支持 --json（原样打印 data）。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 通用小工具（cmpv2 前缀，避免与其它代理撞名）

// cmpv2MaxDepth 人类可读打印的嵌套深度上限（无障碍树可能非常深）。
const cmpv2MaxDepth = 12

// cmpv2Scalar 判断是否标量，并给出显示文本。
func cmpv2Scalar(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "null", true
	case string:
		return t, true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	default:
		return "", false
	}
}

// cmpv2SortedKeys 固定键序，保证输出可复现。
func cmpv2SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cmpv2Abbrev 超长 base64（include_screenshot 的 PNG）在人类可读模式下省略，--json 仍可看原文。
func cmpv2Abbrev(key, s string) string {
	if key == "screenshot" && len(s) > 200 {
		return fmt.Sprintf("<base64，%d 字符已省略；用 --json 查看原文>", len(s))
	}
	return s
}

// cmpv2Human 人类可读打印任意 JSON data。
func cmpv2Human(v any) { cmpv2Print(v, "", 0) }

// cmpv2Print 递归打印：map 按键排序；list 逐项 "- "；空容器内联展示。
func cmpv2Print(v any, indent string, depth int) {
	if depth > cmpv2MaxDepth {
		fmt.Printf("%s…（超过 %d 层，省略更深内容）\n", indent, cmpv2MaxDepth)
		return
	}
	switch t := v.(type) {
	case map[string]any:
		cmpv2PrintMap(t, indent, "", depth)
	case []any:
		if len(t) == 0 {
			fmt.Printf("%s[]\n", indent)
			return
		}
		for _, it := range t {
			if s, ok := cmpv2Scalar(it); ok {
				fmt.Printf("%s- %s\n", indent, s)
				continue
			}
			if m, ok := it.(map[string]any); ok {
				// 列表内 map：首行接 "- "，后续行与首行对齐
				cmpv2PrintMap(m, indent, "- ", depth)
				continue
			}
			// 嵌套列表等少见形态：兜底缩进
			fmt.Printf("%s-\n", indent)
			cmpv2Print(it, indent+"  ", depth+1)
		}
	default:
		if s, ok := cmpv2Scalar(v); ok {
			fmt.Printf("%s%s\n", indent, s)
		}
	}
}

// cmpv2PrintMap 打印一个 map；first 用于列表项首行（"- "）。
func cmpv2PrintMap(m map[string]any, indent, first string, depth int) {
	if len(m) == 0 {
		fmt.Printf("%s%s{}\n", indent, first)
		return
	}
	for i, k := range cmpv2SortedKeys(m) {
		p := ""
		if i == 0 {
			p = first
		}
		cv := m[k]
		if s, ok := cmpv2Scalar(cv); ok {
			fmt.Printf("%s%s%s: %s\n", indent, p, k, cmpv2Abbrev(k, s))
			continue
		}
		if inner, ok := cv.(map[string]any); ok && len(inner) == 0 {
			fmt.Printf("%s%s%s: {}\n", indent, p, k)
			continue
		}
		if inner, ok := cv.([]any); ok && len(inner) == 0 {
			fmt.Printf("%s%s%s: []\n", indent, p, k)
			continue
		}
		fmt.Printf("%s%s%s:\n", indent, p, k)
		cmpv2Print(cv, indent+"  ", depth+1)
	}
}

// cmpv2Q 仅当值非空时写入 query——不替用户塞默认值（服务端自带默认值）。
func cmpv2Q(q map[string]string, key, val string) {
	if v := strings.TrimSpace(val); v != "" {
		q[key] = v
	}
}

// cmpv2IntQ 解析非负整数 flag 写入 query；未给/空 = 跳过；非法 = 明确报错。
func cmpv2IntQ(q map[string]string, flags map[string]string, flag, key string) error {
	v, ok := flags[flag]
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return fmt.Errorf("--%s 需为非负整数（得到 %q）", flag, v)
	}
	q[key] = strconv.Itoa(n)
	return nil
}

// cmpv2ParseBool 接受 true/false/1/0/yes/no/on/off（裸 flag 由 splitFlags 记为 "true"）。
func cmpv2ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on", "":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("不是布尔值（%q）", s)
}

// cmpv2BoolQ 布尔 flag → query（显式 true/false 都发送；未给则不发）。
func cmpv2BoolQ(q map[string]string, flags map[string]string, flag, key string) error {
	v, ok := flags[flag]
	if !ok {
		return nil
	}
	b, err := cmpv2ParseBool(v)
	if err != nil {
		return fmt.Errorf("--%s 需为 true/false（得到 %q）", flag, v)
	}
	q[key] = strconv.FormatBool(b)
	return nil
}

// cmpv2Get 只读命令（cursor/clipboard/windows）的公共实现。
func cmpv2Get(path, what string, args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", path, nil, nil)
	if err != nil {
		return fmt.Errorf("%s失败（GET %s）: %w", what, path, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// ---------------------------------------------------------------- 无障碍（accessibility）

// cmpv2A11yQuery 由 flags 生成无障碍两个路由的 query（只发用户显式给出的项）。
// 与 openapi 对齐：scope / max_depth / max_nodes / role / name / match / states /
// include_offscreen / timeout_ms（nodes 额外 limit / node_id）。
func cmpv2A11yQuery(flags map[string]string, withPaging bool) (map[string]string, error) {
	q := map[string]string{}
	if v := strings.TrimSpace(flags["scope"]); v != "" {
		if v != "foreground" && v != "desktop" {
			return nil, fmt.Errorf("--scope 仅支持 foreground|desktop（得到 %q）", v)
		}
		q["scope"] = v
	}
	if err := cmpv2IntQ(q, flags, "max-depth", "max_depth"); err != nil {
		return nil, err
	}
	if err := cmpv2IntQ(q, flags, "max-nodes", "max_nodes"); err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(flags["role"]); v != "" {
		q["role"] = v
	}
	if v := strings.TrimSpace(flags["name"]); v != "" {
		q["name"] = v
	}
	if v := strings.TrimSpace(flags["match"]); v != "" {
		switch v {
		case "exact", "substring", "regex":
			q["match"] = v
		default:
			return nil, fmt.Errorf("--match 仅支持 exact|substring|regex（得到 %q）", v)
		}
	}
	if v := strings.TrimSpace(flags["states"]); v != "" {
		// 服务端定义为逗号分隔的状态列表（如 enabled,showing），原样透传。
		q["states"] = v
	}
	if err := cmpv2BoolQ(q, flags, "include-offscreen", "include_offscreen"); err != nil {
		return nil, err
	}
	if err := cmpv2IntQ(q, flags, "timeout-ms", "timeout_ms"); err != nil {
		return nil, err
	}
	if withPaging {
		if err := cmpv2IntQ(q, flags, "limit", "limit"); err != nil {
			return nil, err
		}
		if v := strings.TrimSpace(flags["node-id"]); v != "" {
			q["node_id"] = v
		}
	}
	return q, nil
}

// cmpv2A11yRun 两个无障碍命令的公共实现。
func cmpv2A11yRun(path, what string, args []string, withPaging bool) error {
	_, flags := splitFlags(args)
	q, err := cmpv2A11yQuery(flags, withPaging)
	if err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", path, q, nil)
	if err != nil {
		return fmt.Errorf("%s失败（GET %s）: %w", what, path, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// cmdCmpA11y 完整无障碍树（GET /v2/computer/accessibility）。
func cmdCmpA11y(args []string) error {
	return cmpv2A11yRun("/v2/computer/accessibility", "读取无障碍树", args, false)
}

// cmdCmpA11yNodes 扁平节点查询（GET /v2/computer/accessibility/nodes，支持 limit/node_id）。
func cmdCmpA11yNodes(args []string) error {
	return cmpv2A11yRun("/v2/computer/accessibility/nodes", "查询无障碍节点", args, true)
}

// ---------------------------------------------------------------- 动作（actions / actions/batch）

// cmpv2ActionVariants v2 DisplayAction 的全部 action_type 枚举（openapi 实测 22 种）。
var cmpv2ActionVariants = map[string]bool{
	"MOVE_TO": true, "MOVE_REL": true, "CLICK": true, "MOUSE_DOWN": true, "MOUSE_UP": true,
	"RIGHT_CLICK": true, "DOUBLE_CLICK": true, "DRAG_TO": true, "DRAG_REL": true, "SCROLL": true,
	"TYPING": true, "PRESS": true, "KEY_DOWN": true, "KEY_UP": true, "HOTKEY": true, "WAIT": true,
	"SET_CLIPBOARD": true, "WINDOW_ACTIVATE": true, "WINDOW_MINIMIZE": true, "NODE_FOCUS": true,
	"NODE_INVOKE": true, "NODE_SET_VALUE": true,
}

// cmpv2ActionAliases v1/OSWorld 风格动作名 → v2 枚举（追加 action_type 时使用）。
var cmpv2ActionAliases = map[string]string{
	"move": "MOVE_TO", "move_to": "MOVE_TO", "moveto": "MOVE_TO", "mouse_move": "MOVE_TO",
	"move_rel": "MOVE_REL", "moverel": "MOVE_REL",
	"click": "CLICK", "left_click": "CLICK", "leftclick": "CLICK",
	"right_click": "RIGHT_CLICK", "rightclick": "RIGHT_CLICK",
	"double_click": "DOUBLE_CLICK", "doubleclick": "DOUBLE_CLICK", "left_double": "DOUBLE_CLICK",
	"mouse_down": "MOUSE_DOWN", "left_mouse_down": "MOUSE_DOWN",
	"mouse_up": "MOUSE_UP", "left_mouse_up": "MOUSE_UP",
	"drag": "DRAG_TO", "drag_to": "DRAG_TO", "dragto": "DRAG_TO",
	"drag_rel": "DRAG_REL", "dragrel": "DRAG_REL",
	"scroll": "SCROLL", "wheel": "SCROLL",
	"type": "TYPING", "typing": "TYPING", "write": "TYPING", "input_text": "TYPING",
	"press": "PRESS", "key": "PRESS", "key_down": "KEY_DOWN", "key_up": "KEY_UP",
	"hotkey": "HOTKEY", "wait": "WAIT", "sleep": "WAIT",
	"set_clipboard": "SET_CLIPBOARD", "clipboard": "SET_CLIPBOARD",
	"window_activate": "WINDOW_ACTIVATE", "activate_window": "WINDOW_ACTIVATE",
	"window_minimize": "WINDOW_MINIMIZE", "minimize_window": "WINDOW_MINIMIZE",
	"node_focus": "NODE_FOCUS", "node_invoke": "NODE_INVOKE", "node_set_value": "NODE_SET_VALUE",
}

// cmpv2ActionType 把动作名映射为 v2 action_type；无法识别返回 ("", false)。
func cmpv2ActionType(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.NewReplacer("-", "_", " ", "_").Replace(n)
	if v, ok := cmpv2ActionAliases[n]; ok {
		return v, true
	}
	up := strings.ToUpper(n)
	if cmpv2ActionVariants[up] {
		return up, true
	}
	return "", false
}

// cmpv2PosAction 该动作是否使用屏幕坐标 x/y（coordinate→x/y 转换仅对这些动作生效）。
func cmpv2PosAction(at string) bool {
	switch at {
	case "MOVE_TO", "CLICK", "RIGHT_CLICK", "DOUBLE_CLICK", "DRAG_TO":
		return true
	}
	return false
}

// cmpv2NormAction 加法式归一化单个动作（绝不删除用户的字段）：
//
//	① 已有 action_type → 原样透传（完全自由结构）；
//	② 有可识别的 "action" → 追加 action_type（click → CLICK，left_click → CLICK …）；
//	③ 归一化后是坐标类动作且缺 x/y、但有 coordinate=[x,y] → 追加 x/y。
//
// 无法识别时原样透传，让服务端给出精确的 422 校验信息。
func cmpv2NormAction(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("动作必须是 JSON 对象，例如 '{\"action\":\"click\",\"x\":100,\"y\":200}'")
	}
	if _, has := m["action_type"]; has {
		return m, nil
	}
	name, _ := m["action"].(string)
	at, ok := cmpv2ActionType(name)
	if !ok {
		return m, nil
	}
	m["action_type"] = at
	if cmpv2PosAction(at) {
		_, hasX := m["x"]
		_, hasY := m["y"]
		if !hasX || !hasY {
			if c, ok := m["coordinate"].([]any); ok && len(c) == 2 {
				if !hasX {
					m["x"] = c[0]
				}
				if !hasY {
					m["y"] = c[1]
				}
			}
		}
	}
	return m, nil
}

// cmdCmpAct 单个动作（POST /v2/computer/actions?include_screenshot=…）。
func cmdCmpAct(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "cmp-act '<JSON 动作>' [--screenshot] [--json]（例：'{\"action\":\"click\",\"x\":100,\"y\":200}'）"); err != nil {
		return err
	}
	v, err := jsonArg(pos[0])
	if err != nil {
		return err
	}
	act, err := cmpv2NormAction(v)
	if err != nil {
		return err
	}
	q := map[string]string{}
	if flagBool(flags, "screenshot") {
		q["include_screenshot"] = "true"
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/computer/actions", q, act)
	if err != nil {
		return fmt.Errorf("执行动作失败（POST /v2/computer/actions）: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// cmdCmpActBatch 批量动作（POST /v2/computer/actions/batch，body={actions, include_screenshot}）。
func cmdCmpActBatch(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "cmp-act-batch '<JSON 动作数组>' [--screenshot] [--json]"); err != nil {
		return err
	}
	v, err := jsonArg(pos[0])
	if err != nil {
		return err
	}
	lst, ok := v.([]any)
	if !ok {
		return fmt.Errorf("批量动作需要 JSON 数组，例如 '[{\"action\":\"click\",\"x\":1,\"y\":2}]'")
	}
	acts := make([]any, 0, len(lst))
	for i, it := range lst {
		m, err := cmpv2NormAction(it)
		if err != nil {
			return fmt.Errorf("第 %d 个动作无效: %w", i+1, err)
		}
		acts = append(acts, m)
	}
	// include_screenshot 是 batch 请求体的正式字段（openapi：DisplayActionBatchRequest），
	// 因此始终显式发送；与单动作把它作为 query 参数的处理方式不同（对应服务端设计差异）。
	body := map[string]any{"actions": acts, "include_screenshot": flagBool(flags, "screenshot")}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/computer/actions/batch", nil, body)
	if err != nil {
		return fmt.Errorf("批量执行动作失败（POST /v2/computer/actions/batch）: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// ---------------------------------------------------------------- 录制（record）

// cmpv2IntBody 整数 flag → body（未给/空跳过；非法报错；min 为下限）。
func cmpv2IntBody(body map[string]any, flags map[string]string, flag, key string, min int) error {
	v, ok := flags[flag]
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < min {
		return fmt.Errorf("--%s 需为不小于 %d 的整数（得到 %q）", flag, min, v)
	}
	body[key] = n
	return nil
}

// cmdCmpRecord 屏幕录制 start/stop（POST /v2/computer/record）。
// 只发送用户显式给出的字段（openapi：DisplayRecordRequest 无必填项，缺省由服务端兜底）。
func cmdCmpRecord(args []string) error {
	_, flags := splitFlags(args)
	body := map[string]any{}
	action := strings.TrimSpace(flags["action"])
	if action == "" {
		action = "start" // 用法为 start|stop，未指定时按 start 处理
	}
	body["action"] = action
	if err := cmpv2IntBody(body, flags, "fps", "fps", 1); err != nil {
		return err
	}
	if err := cmpv2IntBody(body, flags, "crf", "crf", 0); err != nil {
		return err
	}
	if err := cmpv2IntBody(body, flags, "width", "width", 1); err != nil {
		return err
	}
	if err := cmpv2IntBody(body, flags, "height", "height", 1); err != nil {
		return err
	}
	if v, ok := flags["max-duration"]; ok && strings.TrimSpace(v) != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f <= 0 {
			return fmt.Errorf("--max-duration 需为正数（秒，得到 %q）", v)
		}
		body["max_duration"] = f
	}
	if v := strings.TrimSpace(flags["save-path"]); v != "" {
		body["save_path"] = v
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/computer/record", nil, body)
	if err != nil {
		return fmt.Errorf("屏幕录制失败（POST /v2/computer/record, action=%s）: %w", action, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// ---------------------------------------------------------------- 信息与截图

// cmdCmpInfo 桌面信息（GET /v2/computer/info）。
func cmdCmpInfo(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/computer/info", nil, nil)
	if err != nil {
		return fmt.Errorf("读取桌面信息失败（GET /v2/computer/info，需要 aio-computer 镜像）: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cmpv2Human(data)
	return nil
}

// cmpv2PNGSize 读取 PNG IHDR 中的宽高（非 PNG 或结构异常时 ok=false）。
func cmpv2PNGSize(b []byte) (int, int, bool) {
	if len(b) < 24 || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	w := int(b[16])<<24 | int(b[17])<<16 | int(b[18])<<8 | int(b[19])
	h := int(b[20])<<24 | int(b[21])<<16 | int(b[22])<<8 | int(b[23])
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// cmdCmpShot 截图（GET /v2/computer/screenshot 的原始 image/png 字节）写盘。
// 先校验 PNG 魔数，避免把 503/错误页写入 .png；再尝试解析 IHDR 宽高。
func cmdCmpShot(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "cmp-shot <输出文件.png> [--json]"); err != nil {
		return err
	}
	out := pos[0]
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("GET", "/v2/computer/screenshot", nil, nil, 300*time.Second)
	if err != nil {
		return fmt.Errorf("下载截图失败（GET /v2/computer/screenshot，需要 aio-computer 镜像）: %w", err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		head := raw
		if len(head) > 16 {
			head = head[:16]
		}
		return fmt.Errorf("截图响应不是 PNG（%d 字节，前 16 字节: % x）——可能未命中 computer-use worker", len(raw), head)
	}
	w, h, hasSize := cmpv2PNGSize(raw)
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return fmt.Errorf("写入截图文件失败（%s）: %w", out, err)
	}
	abs := out
	if p, err2 := filepath.Abs(out); err2 == nil {
		abs = p
	}
	if flagBool(flags, "json") {
		sum := map[string]any{"file": abs, "bytes": len(raw), "format": "image/png"}
		if hasSize {
			sum["width"] = w
			sum["height"] = h
		}
		printJSON(sum)
		return nil
	}
	if hasSize {
		fmt.Printf("已保存截图: %s（%d 字节，PNG %dx%d）\n", abs, len(raw), w, h)
	} else {
		fmt.Printf("已保存截图: %s（%d 字节，PNG）\n", abs, len(raw))
	}
	return nil
}

// ---------------------------------------------------------------- 三个只读命令

// cmdCmpCursor 鼠标位置（GET /v2/computer/cursor）。
func cmdCmpCursor(args []string) error {
	return cmpv2Get("/v2/computer/cursor", "读取鼠标位置", args)
}

// cmdCmpClipboard 剪贴板（GET /v2/computer/clipboard）。
func cmdCmpClipboard(args []string) error {
	return cmpv2Get("/v2/computer/clipboard", "读取剪贴板", args)
}

// cmdCmpWindows 窗口列表（GET /v2/computer/windows）。
func cmdCmpWindows(args []string) error {
	return cmpv2Get("/v2/computer/windows", "枚举窗口", args)
}
