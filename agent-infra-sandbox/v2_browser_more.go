// v2_browser_more.go —— 浏览器·文件上传 / 浏览器配置（纯 v2 API）
//
// 路由（见 V2-API.md；下列"实测"基于 aiod 0.9.2，2026-10-03 在 CubeSandbox 内验证）：
//
//	POST /v2/browser/upload  body=UploadRequest{paths*:array, selector?:string, ref?:string, tab_id?:string}
//	    —— paths 是**服务端所在沙箱内**的文件路径（不是把本地文件传进沙箱）；成功返回 data={"count":N}。
//	       实测（真机 E2E）：data:URL 页面 + <input type=file id=f> 可用，页面内 files[0].name 正确；
//	       缺 selector/ref → HTTP 400 "provide `selector` or `ref`"；
//	       两者都给时服务端优先走 ref（实测 503 "DOM.setFileInputFiles: No node found..."），故本命令要求二选一；
//	       paths 指向不存在的文件时服务端不报错（实测仍 count=1，input.files[0].name=路径 basename）。
//	POST /v2/browser/config  body=BrowserConfigRequest{resolution?:Resolution{width?,height?}}
//	    —— 实测：resolution 必须是**对象** {"width":W,"height":H}；
//	       传字符串 "1024x768" 会被 422 拒绝（invalid type: string "1024x768", expected struct Resolution）。
//	       故 --resolution 解析出 W/H 后生成对象；--json 里出现的 "WxH" 字符串也会按此契约归一化为对象
//	       （stderr 提示一次；其余字段与其它情况一律原样透传）。
//	       成功时 data=null、服务端 message="Resolution set to WxH"；未知字段被忽略（bogus-only → "No configuration changes provided"）。
//
// 输出约定：每个命令都支持 --json（置位时原样打印 data）；辅助函数统一 brm 前缀防撞名。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// cmdBrUpload：POST /v2/browser/upload —— 把沙箱内文件关联到页面 <input type=file>。
// 用法：br-upload (--selector= | --ref=) --paths=<沙箱内文件1,文件2> [--tab-id=] [--json]
func cmdBrUpload(args []string) error {
	_, flags := splitFlags(args)
	sel := strings.TrimSpace(flags["selector"])
	ref := strings.TrimSpace(flags["ref"])
	if sel == "" && ref == "" {
		return fmt.Errorf("需要 --selector= 或 --ref= 指定目标文件输入框，用法: br-upload (--selector= | --ref=) --paths=<沙箱内文件1,文件2> [--tab-id=] [--json]")
	}
	if sel != "" && ref != "" {
		// 实测：两者都传时服务端优先走 ref，容易得到与 selector 无关的报错，这里直接拦下更清晰。
		return fmt.Errorf("--selector 与 --ref 只能给一个（服务端同时收到时会优先走 ref），用法: br-upload (--selector= | --ref=) --paths=<沙箱内文件1,文件2> [--tab-id=] [--json]")
	}
	raw := flags["paths"]
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("缺少 --paths=<沙箱内文件1,文件2>（服务端所在沙箱内的文件路径），用法: br-upload (--selector= | --ref=) --paths=<沙箱内文件1,文件2> [--tab-id=] [--json]")
	}
	paths := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("--paths 解析后为空，用法: br-upload (--selector= | --ref=) --paths=<沙箱内文件1,文件2> [--tab-id=] [--json]")
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") {
			fmt.Fprintf(os.Stderr, "提示: paths 含相对路径 %q，服务端按其工作目录解析，建议用绝对路径\n", p)
		}
	}

	body := map[string]any{"paths": paths}
	if sel != "" {
		body["selector"] = sel
	}
	if ref != "" {
		body["ref"] = ref
	}
	if tab := brmTabID(flags); tab != "" {
		body["tab_id"] = tab
	}

	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/upload", nil, body)
	if err != nil {
		target := sel
		if target == "" {
			target = ref
		}
		return fmt.Errorf("浏览器文件上传失败（target=%s paths=%s）: %w", target, strings.Join(paths, ","), err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印（实测 data={"count":N}）
		return nil
	}
	cnt := len(paths)
	if m := asMap(data); m != nil {
		if _, ok := m["count"]; ok {
			cnt = asInt(m["count"], len(paths))
		}
	}
	target := sel
	if target == "" {
		target = ref
	}
	fmt.Printf("已关联 %d 个文件到 %s: %s\n", cnt, target, strings.Join(paths, ", "))
	return nil
}

// cmdBrConfig：POST /v2/browser/config —— 应用浏览器配置。
// body 来源：--json='{...}'（JSON 对象，优先）或 --resolution=<宽>x<高>。
// 注意 --json 兼作通用"JSON 输出"开关：带值时它是请求体（输出也是 JSON），不带值时只切换输出格式。
// 用法：br-config [--resolution=1280x1024] | [--json='{...}']
func cmdBrConfig(args []string) error {
	_, flags := splitFlags(args)
	jsonVal, hasJSONVal := flags["json"]
	// 带值的 --json 才是请求体；裸 --json / --json=true / --json=false 只是输出开关。
	bodyFromJSON := hasJSONVal && jsonVal != "" && jsonVal != "true" && jsonVal != "false"

	var body map[string]any
	if bodyFromJSON {
		v, err := jsonArg(jsonVal)
		if err != nil {
			return fmt.Errorf("--json 解析失败: %w", err)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("--json 需为 JSON 对象（BrowserConfigRequest），得到 %s", brmTypeName(v))
		}
		// 实测契约：resolution 必须是 struct（对象）；字符串 "WxH" 会被服务端 422 拒绝，
		// 这里按实测归一化为 {"width":W,"height":H}，其余内容原样透传。
		if s, ok := m["resolution"].(string); ok {
			w, h, err := brmParseWH(s)
			if err != nil {
				return fmt.Errorf("--json 里的 resolution 字符串无效: %w", err)
			}
			m["resolution"] = map[string]any{"width": w, "height": h}
			fmt.Fprintf(os.Stderr, "注: resolution=%q 已按服务端契约归一化为 {\"width\":%d,\"height\":%d}（字符串形式会被 422 拒绝）\n", s, w, h)
		}
		body = m
	} else if res := strings.TrimSpace(flags["resolution"]); res != "" {
		w, h, err := brmParseWH(res)
		if err != nil {
			return fmt.Errorf("--resolution 无效: %w", err)
		}
		body = map[string]any{"resolution": map[string]any{"width": w, "height": h}}
	} else {
		return fmt.Errorf("缺少配置：用 --resolution=<宽>x<高>（如 1280x1024）或 --json='{...}'，用法: br-config [--resolution=1280x1024] | [--json='{...}']")
	}

	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/config", nil, body)
	if err != nil {
		return fmt.Errorf("应用浏览器配置失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印（实测成功时 data=null）
		return nil
	}
	if res, ok := asMap(body)["resolution"].(map[string]any); ok && len(body) == 1 {
		fmt.Printf("分辨率已设置: %dx%d（服务端已确认）\n", asInt(res["width"], 0), asInt(res["height"], 0))
		return nil
	}
	b, _ := json.Marshal(body)
	fmt.Printf("浏览器配置已应用: %s\n", string(b))
	return nil
}

// ---------------------------------------------------------------- 内部工具（brm 前缀防撞名）

// brmParseWH 解析 "宽x高"（兼容 x / X / ×，允许空白），返回正整数宽高。
func brmParseWH(s string) (int, int, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' || r == '×' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q 不是 <宽>x<高> 形式（如 1280x1024）", s)
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("%q 中的宽高需为正整数（如 1280x1024）", s)
	}
	return w, h, nil
}

// brmTabID 依次从 --tab-id= / --tab_id= / --tab= 取标签页 id（为空表示作用于当前活动页）。
func brmTabID(flags map[string]string) string {
	for _, k := range []string{"tab-id", "tab_id", "tab"} {
		if v := strings.TrimSpace(flags[k]); v != "" {
			return v
		}
	}
	return ""
}

// brmTypeName 给 --json 类型错误用的可读类型名。
func brmTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "字符串"
	case float64:
		return "数字"
	case bool:
		return "布尔值"
	case []any:
		return "数组"
	default:
		return fmt.Sprintf("%T", v)
	}
}
