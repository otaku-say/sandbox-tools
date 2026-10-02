// cmd_browser.go — browser + browserpage 两个命名空间的 CLI 封装（6 + 29 = 35 个方法）。
//
// 动作清单（与 dispatch.go 顶部注释保持一致）：
//
//	browser info|config|restart|screenshot|action|pac
//	page    navigate|back|forward|reload|click|fill|type|press|hotkey|hover|select|check|uncheck|
//	        upload|fill-form|scroll|scroll-to|scroll-to-element|screenshot|get-html|get-text|get-markdown|
//	        elements|console|export-console|evaluate|find-text|wait|record
//
// 约定：选项写成 --key=value（布尔写 --flag）；位置参数优先、选项作补充；
// 结果统一 printJSON(resp)；截图等流式响应（io.Reader）写入 --out 指定的本地文件
// （默认 shot.png），stdout 只打印保存路径。字段/类型逐字对齐 API-REFERENCE.md §3.8/§3.9。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

const browserUsageText = `用法: sandbox-sdk-go browser <action> [参数]

  info                                     浏览器信息（CDP/VNC URL、视口尺寸）
  config --resolution=1920x1080            设置分辨率（或 --width=1920 --height=1080）
  restart [--mode=soft|hard] [--blocklist=a,b] [--allowlist=a,b] [--locale=zh-CN]
                                           重启浏览器
  screenshot [--out=shot.png]              截取屏幕画面（默认保存 shot.png，打印路径）
  action --type=<类型> [...]               执行单个动作（ExecuteAction）
         类型: move_to|move_rel|click|mouse_down|mouse_up|right_click|double_click|
               drag_to|drag_rel|scroll|typing|press|key_down|key_up|hotkey|wait
         选项: --x --y --dx --dy --button=left|right|middle --clicks --x-offset --y-offset
               --text --key --keys=a,b|A+b --duration=秒 --use-clipboard
  pac                                      获取代理 PAC（该方法只返回 error，无响应体）
`

const pageUsageText = `用法: sandbox-sdk-go page <action> [参数]

  navigate <url> [--wait-until=load|domcontentloaded|networkidle|commit] [--timeout=秒]
  back | forward | reload
  click [selector] [--selector= --index= --x= --y= --button=left|right|middle --count=]
  fill <selector> <text> [--index=]
  type <text> [--delay=秒]
  press <key>
  hotkey "Control+c"（或 --keys=Control,c）
  hover [selector] [--x= --y=]
  select <selector> [value] [--value= --label= --index=]
  check <selector> | uncheck <selector>
  upload <selector> <file>[,<file>...]
  fill-form '<json>'（或 --items='[{"selector":"#a","text":"x"}]'）
  scroll <up|down|left|right> [--amount=像素]
  scroll-to [x] [y]（或 --x= --y=）
  scroll-to-element <selector>
  screenshot [--full-page] [--format=png|jpeg] [--quality=90] [--out=shot.png]
  get-html [--outer] | get-text | get-markdown | elements
  console [--clear]
  export-console <save_path> [--clear]
  evaluate <js表达式>
  find-text <关键词>
  wait <selector|load|url|network_idle|download|function|response|request|timeout> [值]
       [--timeout=秒] [--state=] [--polling=] [--save-path=] [--url-pattern=]
  record [once|start|pause|resume|stop|status] [--save-path=] [--duration=秒] [--fps=] [--quality=]
`

// ---------- 通用辅助 ----------

// browserUsageOut 输出用法文本：code==0 走 stdout（用户主动查帮助），否则走 stderr（用法错误）。
func browserUsageOut(code int, text string) {
	if code == 0 {
		fmt.Print(text)
	} else {
		fmt.Fprint(os.Stderr, text)
	}
	os.Exit(code)
}

// browserOptStr 空串转 nil，非空用 SDK 指针辅助 sandboxsdkgo.String。
func browserOptStr(s string) *string {
	if s == "" {
		return nil
	}
	return sandboxsdkgo.String(s)
}

// browserPick 优先取第 i 个位置参数，缺省回落到 --<key> 选项。
func browserPick(pos []string, i int, flags map[string]string, key string) string {
	if i < len(pos) && pos[i] != "" {
		return pos[i]
	}
	return fget(flags, key, "")
}

// browserNeed 取必填参数（位置参数优先，其次 --<key>）；缺失则打印用法并退出。
func browserNeed(pos []string, i int, flags map[string]string, key, usage string) string {
	if v := browserPick(pos, i, flags, key); v != "" {
		return v
	}
	fatal("%s", usage)
	return ""
}

// browserIntArg 取必填整数参数（位置参数或 --<key>）。
func browserIntArg(pos []string, i int, flags map[string]string, key, usage string) int {
	s := browserNeed(pos, i, flags, key, usage)
	n, err := strconv.Atoi(s)
	if err != nil {
		fatal("需要整数参数，得到 %q —— %s", s, usage)
	}
	return n
}

// browserOptInt 取可选整数（位置参数或 --<key>）；未提供返回 nil。
func browserOptInt(pos []string, i int, flags map[string]string, key string) *int {
	s := browserPick(pos, i, flags, key)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		fatal("需要整数参数，得到 %q", s)
	}
	return sandboxsdkgo.Int(n)
}

// browserOptFloat 取可选浮点数（位置参数或 --<key>）；未提供返回 nil。
func browserOptFloat(pos []string, i int, flags map[string]string, key string) *float64 {
	s := browserPick(pos, i, flags, key)
	if s == "" {
		return nil
	}
	x, err := strconv.ParseFloat(s, 64)
	if err != nil {
		fatal("需要数值参数，得到 %q", s)
	}
	return sandboxsdkgo.Float64(x)
}

// browserNeedFloat 取必填浮点选项。
func browserNeedFloat(flags map[string]string, key, usage string) float64 {
	v := fget(flags, key, "")
	if v == "" {
		fatal("%s", usage)
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		fatal("--%s 需要数值，得到 %q", key, v)
	}
	return x
}

// browserNeedStr 取必填字符串选项。
func browserNeedStr(flags map[string]string, key, usage string) string {
	v := fget(flags, key, "")
	if v == "" {
		fatal("%s", usage)
	}
	return v
}

// browserSaveReader 把流式响应写入 --out 指定的本地文件（默认 def），stdout 打印保存路径。
func browserSaveReader(reader io.Reader, flags map[string]string, def string) {
	out := fget(flags, "out", def)
	data, err := io.ReadAll(reader)
	if err != nil {
		fatal("读取响应流失败: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fatal("写入 %s 失败: %v", out, err)
	}
	fmt.Println(out)
	fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 已保存 %d 字节\n", len(data))
}

// browserJSON 解析 JSON 字符串。
func browserJSON(s string, v any) {
	if err := json.Unmarshal([]byte(s), v); err != nil {
		fatal("JSON 解析失败: %v", err)
	}
}

// browserJSONItems 解析 JSON：数组 → 多项；单对象 → 单项（fill-form / cookies set 共用）。
func browserJSONItems(raw string) []map[string]any {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		one := map[string]any{}
		browserJSON(trimmed, &one)
		return []map[string]any{one}
	}
	items := []map[string]any{}
	browserJSON(trimmed, &items)
	return items
}

// browserItems 解析 fill-form 条目（--items/--json 或第一个位置参数）。
func browserItems(flags map[string]string, pos []string) []map[string]any {
	raw := fget(flags, "items", "")
	if raw == "" {
		raw = fget(flags, "json", "")
	}
	if raw == "" && len(pos) > 0 {
		raw = pos[0]
	}
	if raw == "" {
		fatal("用法: sandbox-sdk-go page fill-form '<json>' 或 --items=<json>")
	}
	items := browserJSONItems(raw)
	if len(items) == 0 {
		fatal("fill-form 条目为空")
	}
	return items
}

// browserKVFrom 把 key=value 位置参数、--<flagKey>（逗号分隔）与 --json 合并为 map；
// 无任何输入时返回 nil（由调用方决定是否必填）。
func browserKVFrom(items []string, flags map[string]string, flagKey string) map[string]string {
	if v := fget(flags, "json", ""); v != "" {
		out := map[string]string{}
		browserJSON(v, &out)
		return out
	}
	if raw := fget(flags, flagKey, ""); raw != "" {
		items = append(items, strings.Split(raw, ",")...)
	}
	if len(items) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, item := range items {
		k, v, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(k) == "" {
			fatal("需要 key=value 形式，得到 %q", item)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// browserSplitKeys 拆分组合键："Control+c" / "Control,c" → ["Control","c"]。
func browserSplitKeys(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == '+' || r == ',' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if t := strings.TrimSpace(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// browserSplitComma 按逗号拆分并丢弃空白项。
func browserSplitComma(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// browserParseResolution 解析 "1920x1080" 形式的分辨率。
func browserParseResolution(s string) (int, int, bool) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(s)), "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, false
	}
	return w, h, true
}

// ---------- browser 命名空间（6） ----------

// cmdBrowser 实现 browser：info / config / restart / screenshot / action / pac。
func cmdBrowser(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, browserUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "info":
		resp, err := c.Browser.GetInfo(ctx)
		if err != nil {
			fatal("browser info: %v", err)
		}
		printJSON(resp)
	case "config":
		req := &sandboxsdkgo.BrowserConfigRequest{}
		if v := fget(flags, "resolution", ""); v != "" {
			w, h, ok := browserParseResolution(v)
			if !ok {
				fatal("--resolution 形如 1920x1080")
			}
			req.Resolution = &sandboxsdkgo.Resolution{Width: w, Height: h}
		} else if w := fint(flags, "width"); w != nil {
			h := fint(flags, "height")
			if h == nil {
				fatal("--width 需要与 --height 成对指定")
			}
			req.Resolution = &sandboxsdkgo.Resolution{Width: *w, Height: *h}
		} else {
			fatal("用法: sandbox-sdk-go browser config --resolution=1920x1080 或 --width=1920 --height=1080")
		}
		resp, err := c.Browser.SetConfig(ctx, req)
		if err != nil {
			fatal("browser config: %v", err)
		}
		printJSON(resp)
	case "restart":
		req := &sandboxsdkgo.RestartRequest{
			UrlBlocklist: flist(flags, "blocklist"),
			UrlAllowlist: flist(flags, "allowlist"),
			Locale:       browserOptStr(fget(flags, "locale", "")),
		}
		if v := fget(flags, "mode", ""); v != "" {
			m := sandboxsdkgo.Mode(v)
			req.Mode = &m
		}
		resp, err := c.Browser.Restart(ctx, req)
		if err != nil {
			fatal("browser restart: %v", err)
		}
		printJSON(resp)
	case "screenshot":
		reader, err := c.Browser.Screenshot(ctx)
		if err != nil {
			fatal("browser screenshot: %v", err)
		}
		browserSaveReader(reader, flags, "shot.png")
	case "action":
		browserAction(c, flags, pos)
	case "pac":
		if err := c.Browser.GetProxyPac(ctx); err != nil {
			fatal("browser pac: %v", err)
		}
		printJSON(map[string]any{"success": true, "message": "PAC 已获取（SDK GetProxyPac 只返回 error，无响应体）"})
	default:
		browserUsageOut(2, browserUsageText)
	}
}

// browserAction 实现 `browser action`：把命令行参数组装为 SDK 的 Action 联合体（ExecuteAction）。
// 类型名统一规范为 snake_case（如 double-click → double_click）；未识别的类型原样透传 ActionType。
func browserAction(c *client.Client, flags map[string]string, pos []string) {
	const usage = "用法: sandbox-sdk-go browser action --type=<类型> [--x= --y= --dx= --dy= --button= --clicks= --x-offset= --y-offset= --text= --key= --keys= --duration= --use-clipboard]"
	kind := strings.ReplaceAll(strings.ToLower(browserPick(pos, 0, flags, "type")), "-", "_")
	if kind == "" {
		fatal("%s", usage)
	}
	// 注意：SDK 联合体的判别值是大写常量（MOVE_TO / CLICK / DOUBLE_CLICK ...），不是小写
	req := &sandboxsdkgo.Action{ActionType: strings.ToUpper(kind)}
	switch kind {
	case "move_to":
		req.MoveTo = &sandboxsdkgo.MoveToAction{X: browserNeedFloat(flags, "x", usage), Y: browserNeedFloat(flags, "y", usage)}
	case "move_rel":
		req.MoveRel = &sandboxsdkgo.MoveRelAction{XOffset: browserNeedFloat(flags, "x-offset", usage), YOffset: browserNeedFloat(flags, "y-offset", usage)}
	case "click":
		req.Click = &sandboxsdkgo.ClickAction{X: ffloat(flags, "x"), Y: ffloat(flags, "y"), Button: browserButton(flags), NumClicks: fint(flags, "clicks")}
	case "mouse_down":
		req.MouseDown = &sandboxsdkgo.MouseDownAction{Button: browserButton(flags)}
	case "mouse_up":
		req.MouseUp = &sandboxsdkgo.MouseUpAction{Button: browserButton(flags)}
	case "right_click":
		req.RightClick = &sandboxsdkgo.RightClickAction{X: ffloat(flags, "x"), Y: ffloat(flags, "y")}
	case "double_click":
		req.DoubleClick = &sandboxsdkgo.DoubleClickAction{X: ffloat(flags, "x"), Y: ffloat(flags, "y")}
	case "drag_to":
		req.DragTo = &sandboxsdkgo.DragToAction{X: browserNeedFloat(flags, "x", usage), Y: browserNeedFloat(flags, "y", usage)}
	case "drag_rel":
		req.DragRel = &sandboxsdkgo.DragRelAction{XOffset: browserNeedFloat(flags, "x-offset", usage), YOffset: browserNeedFloat(flags, "y-offset", usage)}
	case "scroll":
		req.Scroll = &sandboxsdkgo.ScrollAction{Dx: fint(flags, "dx"), Dy: fint(flags, "dy")}
	case "typing":
		req.Typing = &sandboxsdkgo.TypingAction{Text: browserNeedStr(flags, "text", usage), UseClipboard: fbool(flags, "use-clipboard")}
	case "press":
		req.Press = &sandboxsdkgo.PressAction{Key: browserNeedStr(flags, "key", usage)}
	case "key_down":
		req.KeyDown = &sandboxsdkgo.KeyDownAction{Key: browserNeedStr(flags, "key", usage)}
	case "key_up":
		req.KeyUp = &sandboxsdkgo.KeyUpAction{Key: browserNeedStr(flags, "key", usage)}
	case "hotkey":
		keys := flist(flags, "keys")
		if len(keys) == 0 {
			if raw := browserPick(nil, 0, flags, "key"); raw != "" {
				keys = browserSplitKeys(raw)
			}
		}
		if len(keys) == 0 {
			fatal("%s", usage)
		}
		req.Hotkey = &sandboxsdkgo.HotkeyAction{Keys: keys}
	case "wait":
		req.Wait = &sandboxsdkgo.WaitAction{Duration: browserNeedFloat(flags, "duration", usage)}
	default:
		fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 未识别的动作类型 %q，仅发送 ActionType\n", kind)
	}
	resp, err := c.Browser.ExecuteAction(ctx, req)
	if err != nil {
		fatal("browser action %s: %v", kind, err)
	}
	printJSON(resp)
}

// browserButton 解析 --button=left|right|middle 为 *sandboxsdkgo.Button。
func browserButton(flags map[string]string) *sandboxsdkgo.Button {
	if b := fget(flags, "button", ""); b != "" {
		btn := sandboxsdkgo.Button(b)
		return &btn
	}
	return nil
}

// ---------- browserpage 命名空间（29） ----------

// cmdPage 实现 browserpage：页面导航/交互/表单/滚动/截图/内容提取/控制台/等待/录制。
func cmdPage(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, pageUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "navigate":
		url := browserNeed(pos, 0, flags, "url", "用法: sandbox-sdk-go page navigate <url> [--wait-until=load|domcontentloaded|networkidle|commit] [--timeout=秒]")
		req := &sandboxsdkgo.NavigateRequest{Url: url, Timeout: ffloat(flags, "timeout")}
		if v := fget(flags, "wait-until", ""); v != "" {
			w := sandboxsdkgo.NavigateRequestWaitUntil(v)
			req.WaitUntil = &w
		}
		resp, err := c.BrowserPage.Navigate(ctx, req)
		if err != nil {
			fatal("page navigate: %v", err)
		}
		printJSON(resp)
	case "back", "forward", "reload":
		var resp *sandboxsdkgo.Response
		var err error
		switch action {
		case "back":
			resp, err = c.BrowserPage.Back(ctx)
		case "forward":
			resp, err = c.BrowserPage.Forward(ctx)
		default:
			resp, err = c.BrowserPage.Reload(ctx)
		}
		if err != nil {
			fatal("page %s: %v", action, err)
		}
		printJSON(resp)
	case "click":
		req := &sandboxsdkgo.ClickRequest{
			Selector:   browserOptStr(browserPick(pos, 0, flags, "selector")),
			Index:      fint(flags, "index"),
			X:          ffloat(flags, "x"),
			Y:          ffloat(flags, "y"),
			ClickCount: fint(flags, "count"),
		}
		if b := fget(flags, "button", ""); b != "" {
			req.Button = sandboxsdkgo.String(b)
		}
		if req.Selector == nil && req.X == nil && req.Y == nil {
			fatal("用法: sandbox-sdk-go page click [selector] [--index= --x= --y= --button=left|right|middle --count=]")
		}
		resp, err := c.BrowserPage.Click(ctx, req)
		if err != nil {
			fatal("page click: %v", err)
		}
		printJSON(resp)
	case "fill":
		usage := "用法: sandbox-sdk-go page fill <selector> <text> [--index=]"
		selector := browserPick(pos, 0, flags, "selector")
		text, hasText := "", false
		if len(pos) >= 2 {
			text, hasText = pos[1], true
		}
		if v, ok := flags["text"]; ok {
			text, hasText = v, true
		}
		if selector == "" || !hasText {
			fatal("%s", usage)
		}
		req := &sandboxsdkgo.FillRequest{Selector: sandboxsdkgo.String(selector), Index: fint(flags, "index"), Text: text}
		resp, err := c.BrowserPage.Fill(ctx, req)
		if err != nil {
			fatal("page fill: %v", err)
		}
		printJSON(resp)
	case "type":
		usage := "用法: sandbox-sdk-go page type <text> [--delay=秒]"
		text, hasText := "", false
		if len(pos) >= 1 {
			text, hasText = pos[0], true
		}
		if v, ok := flags["text"]; ok {
			text, hasText = v, true
		}
		if !hasText {
			fatal("%s", usage)
		}
		req := &sandboxsdkgo.TypeTextRequest{Text: text, Delay: ffloat(flags, "delay")}
		resp, err := c.BrowserPage.TypeText(ctx, req)
		if err != nil {
			fatal("page type: %v", err)
		}
		printJSON(resp)
	case "press":
		key := browserNeed(pos, 0, flags, "key", "用法: sandbox-sdk-go page press <key>")
		resp, err := c.BrowserPage.PressKey(ctx, &sandboxsdkgo.KeyRequest{Key: key})
		if err != nil {
			fatal("page press: %v", err)
		}
		printJSON(resp)
	case "hotkey":
		usage := "用法: sandbox-sdk-go page hotkey \"Control+c\"（或 --keys=Control,c）"
		keys := flist(flags, "keys")
		if len(keys) == 0 {
			if raw := browserPick(pos, 0, flags, "key"); raw != "" {
				keys = browserSplitKeys(raw)
			}
		}
		if len(keys) == 0 {
			fatal("%s", usage)
		}
		resp, err := c.BrowserPage.HotKey(ctx, &sandboxsdkgo.HotKeyRequest{Keys: keys})
		if err != nil {
			fatal("page hotkey: %v", err)
		}
		printJSON(resp)
	case "hover":
		req := &sandboxsdkgo.HoverRequest{
			Selector: browserOptStr(browserPick(pos, 0, flags, "selector")),
			X:        ffloat(flags, "x"),
			Y:        ffloat(flags, "y"),
		}
		if req.Selector == nil && req.X == nil && req.Y == nil {
			fatal("用法: sandbox-sdk-go page hover [selector] [--x= --y=]")
		}
		resp, err := c.BrowserPage.Hover(ctx, req)
		if err != nil {
			fatal("page hover: %v", err)
		}
		printJSON(resp)
	case "select":
		usage := "用法: sandbox-sdk-go page select <selector> [value] [--value= --label= --index=]"
		selector := browserNeed(pos, 0, flags, "selector", usage)
		req := &sandboxsdkgo.SelectOptionRequest{Selector: selector, Index: fint(flags, "index")}
		if v := fget(flags, "value", ""); v != "" {
			req.Value = sandboxsdkgo.String(v)
		} else if len(pos) >= 2 {
			req.Value = sandboxsdkgo.String(pos[1])
		}
		req.Label = browserOptStr(fget(flags, "label", ""))
		resp, err := c.BrowserPage.SelectOption(ctx, req)
		if err != nil {
			fatal("page select: %v", err)
		}
		printJSON(resp)
	case "check", "uncheck":
		selector := browserNeed(pos, 0, flags, "selector", "用法: sandbox-sdk-go page "+action+" <selector>")
		var resp *sandboxsdkgo.Response
		var err error
		if action == "check" {
			resp, err = c.BrowserPage.Check(ctx, &sandboxsdkgo.CheckRequest{Selector: selector})
		} else {
			resp, err = c.BrowserPage.Uncheck(ctx, &sandboxsdkgo.CheckRequest{Selector: selector})
		}
		if err != nil {
			fatal("page %s: %v", action, err)
		}
		printJSON(resp)
	case "upload":
		usage := "用法: sandbox-sdk-go page upload <selector> <file>[,<file>...]"
		selector := browserNeed(pos, 0, flags, "selector", usage)
		files := flist(flags, "files")
		if len(files) == 0 {
			for _, p := range pos[1:] {
				files = append(files, browserSplitComma(p)...)
			}
		}
		if len(files) == 0 {
			fatal("%s", usage)
		}
		resp, err := c.BrowserPage.UploadFile(ctx, &sandboxsdkgo.UploadFileRequest{Selector: selector, Files: files})
		if err != nil {
			fatal("page upload: %v", err)
		}
		printJSON(resp)
	case "fill-form":
		req := &sandboxsdkgo.FormFillRequest{Items: browserItems(flags, pos)}
		resp, err := c.BrowserPage.FillForm(ctx, req)
		if err != nil {
			fatal("page fill-form: %v", err)
		}
		printJSON(resp)
	case "scroll":
		usage := "用法: sandbox-sdk-go page scroll <up|down|left|right> [--amount=像素]"
		direction := browserNeed(pos, 0, flags, "direction", usage)
		resp, err := c.BrowserPage.Scroll(ctx, &sandboxsdkgo.ScrollRequest{Direction: sandboxsdkgo.String(direction), Amount: fint(flags, "amount")})
		if err != nil {
			fatal("page scroll: %v", err)
		}
		printJSON(resp)
	case "scroll-to":
		x := browserOptInt(pos, 0, flags, "x")
		y := browserOptInt(pos, 1, flags, "y")
		if x == nil && y == nil {
			fatal("用法: sandbox-sdk-go page scroll-to [x] [y]（或 --x= --y=）")
		}
		resp, err := c.BrowserPage.ScrollTo(ctx, &sandboxsdkgo.ScrollToRequest{X: x, Y: y})
		if err != nil {
			fatal("page scroll-to: %v", err)
		}
		printJSON(resp)
	case "scroll-to-element":
		selector := browserNeed(pos, 0, flags, "selector", "用法: sandbox-sdk-go page scroll-to-element <selector>")
		resp, err := c.BrowserPage.ScrollToElement(ctx, &sandboxsdkgo.ScrollToElementRequest{Selector: selector})
		if err != nil {
			fatal("page scroll-to-element: %v", err)
		}
		printJSON(resp)
	case "screenshot":
		req := &sandboxsdkgo.BrowserPageScreenshotRequest{
			FullPage: fbool(flags, "full-page"),
			Format:   browserOptStr(fget(flags, "format", "")),
			Quality:  fint(flags, "quality"),
		}
		reader, err := c.BrowserPage.Screenshot(ctx, req)
		if err != nil {
			fatal("page screenshot: %v", err)
		}
		browserSaveReader(reader, flags, "shot.png")
	case "get-html":
		resp, err := c.BrowserPage.GetHtml(ctx, &sandboxsdkgo.BrowserPageGetHtmlRequest{Outer: fbool(flags, "outer")})
		if err != nil {
			fatal("page get-html: %v", err)
		}
		printJSON(resp)
	case "get-text":
		resp, err := c.BrowserPage.GetText(ctx)
		if err != nil {
			fatal("page get-text: %v", err)
		}
		printJSON(resp)
	case "get-markdown":
		resp, err := c.BrowserPage.GetMarkdown(ctx)
		if err != nil {
			fatal("page get-markdown: %v", err)
		}
		printJSON(resp)
	case "elements":
		resp, err := c.BrowserPage.GetElements(ctx)
		if err != nil {
			fatal("page elements: %v", err)
		}
		printJSON(resp)
	case "console":
		resp, err := c.BrowserPage.GetConsole(ctx, &sandboxsdkgo.BrowserPageGetConsoleRequest{Clear: fbool(flags, "clear")})
		if err != nil {
			fatal("page console: %v", err)
		}
		printJSON(resp)
	case "export-console":
		path := browserNeed(pos, 0, flags, "save-path", "用法: sandbox-sdk-go page export-console <save_path> [--clear]")
		resp, err := c.BrowserPage.ExportConsole(ctx, &sandboxsdkgo.ExportConsoleLogsRequest{SavePath: path, Clear: fbool(flags, "clear")})
		if err != nil {
			fatal("page export-console: %v", err)
		}
		printJSON(resp)
	case "evaluate":
		expression := browserNeed(pos, 0, flags, "expression", "用法: sandbox-sdk-go page evaluate <js表达式>")
		resp, err := c.BrowserPage.Evaluate(ctx, &sandboxsdkgo.EvaluateRequest{Expression: expression})
		if err != nil {
			fatal("page evaluate: %v", err)
		}
		printJSON(resp)
	case "find-text":
		keyword := browserNeed(pos, 0, flags, "keyword", "用法: sandbox-sdk-go page find-text <关键词>")
		resp, err := c.BrowserPage.FindText(ctx, &sandboxsdkgo.FindTextRequest{Keyword: keyword})
		if err != nil {
			fatal("page find-text: %v", err)
		}
		printJSON(resp)
	case "wait":
		usage := "用法: sandbox-sdk-go page wait <selector|load|url|network_idle|download|function|response|request|timeout> [值] [--timeout=秒] [--state=] [--polling=] [--save-path=] [--url-pattern=]"
		wtype := browserNeed(pos, 0, flags, "type", usage)
		req := &sandboxsdkgo.WaitRequest{
			Type:       sandboxsdkgo.Type(wtype),
			State:      browserOptStr(fget(flags, "state", "")),
			Timeout:    ffloat(flags, "timeout"),
			Polling:    ffloat(flags, "polling"),
			SavePath:   browserOptStr(fget(flags, "save-path", "")),
			UrlPattern: browserOptStr(fget(flags, "url-pattern", "")),
		}
		value := ""
		if len(pos) > 1 {
			value = pos[1]
		}
		switch wtype {
		case "selector":
			req.Selector = browserOptStr(browserPick([]string{value}, 0, flags, "selector"))
			if req.Selector == nil {
				fatal("%s", usage)
			}
		case "url":
			req.Url = browserOptStr(browserPick([]string{value}, 0, flags, "url"))
			if req.Url == nil {
				fatal("%s", usage)
			}
		case "function":
			req.Expression = browserOptStr(browserPick([]string{value}, 0, flags, "expression"))
			if req.Expression == nil {
				fatal("%s", usage)
			}
		case "timeout":
			if req.Timeout == nil && value != "" {
				x, err := strconv.ParseFloat(value, 64)
				if err != nil {
					fatal("wait timeout 需要秒数，得到 %q", value)
				}
				req.Timeout = sandboxsdkgo.Float64(x)
			}
			if req.Timeout == nil {
				fatal("%s", usage)
			}
		}
		resp, err := c.BrowserPage.Wait(ctx, req)
		if err != nil {
			fatal("page wait %s: %v", wtype, err)
		}
		printJSON(resp)
	case "record":
		mode := browserPick(pos, 0, flags, "action")
		if mode == "" {
			mode = "once"
		}
		ra := sandboxsdkgo.RecordRequestAction(mode)
		req := &sandboxsdkgo.RecordRequest{
			Action:   &ra,
			SavePath: browserOptStr(fget(flags, "save-path", "")),
			Duration: ffloat(flags, "duration"),
			Fps:      fint(flags, "fps"),
			Quality:  fint(flags, "quality"),
		}
		resp, err := c.BrowserPage.Record(ctx, req)
		if err != nil {
			fatal("page record: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, pageUsageText)
	}
}
