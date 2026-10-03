// v2_sandbox.go —— 信息面命令：健康检查 / 沙箱自描述 / 已装包清单（纯 v2 API）
//
// 路由（见 V2-API.md）：
//
//	GET /health                —— 平台健康检查（不走统一信封，返回裸 JSON 或文本）
//	GET /v2/sandbox            —— 环境自描述（一把梭）
//	GET /v2/sandbox/packages   —— 某语言运行时的已装包清单（data 是纯文本字符串）
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// cmdHealth 探活。
// 注意：/health 不在统一信封里（实测返回裸 JSON {"status":"healthy",...}），
// 用 apiRaw 取原始字节；非 2xx 时 apiRaw 返回 *APIError，这里直接上抛（退出码非 0）。
// 用法：health [--json]
func cmdHealth(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("GET", "/health", nil, nil, 30*time.Second)
	if err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}
	body := strings.TrimRight(string(raw), "\n")
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if flagBool(flags, "json") {
			printJSON(v)
		} else if asMap(v) != nil {
			sbPrintFlat(v) // 人类可读：平铺 key: value
		} else {
			printJSON(v)
		}
		return nil
	}
	if flagBool(flags, "json") {
		printJSON(body) // 非 JSON 时以 JSON 字符串形式原样输出
	} else {
		fmt.Println(body)
	}
	return nil
}

// cmdSandboxInfo 沙箱环境自描述。
// 默认把关键字段平铺成 "a.b[0].c: 值" 行；--json 原样输出 data。
// 用法：sandbox-info [--json]
func cmdSandboxInfo(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/sandbox", nil, nil)
	if err != nil {
		return fmt.Errorf("查询沙箱信息失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	if asMap(data) == nil {
		printData(data)
		return nil
	}
	sbPrintFlat(data)
	return nil
}

// cmdSandboxPackages 打印某语言运行时的已装包清单。
// 实测：服务端返回信封，data 是**纯文本清单字符串**（不是 JSON 数组），
// 默认原样打印；--json 时把该字符串按 JSON 输出（printJSON）。
// 非 2xx（如 lang=py 会 400）时 api() 返回 *APIError → 非 0 退出。
// 用法：sandbox-packages --lang=python|node [--json]
func cmdSandboxPackages(args []string) error {
	pos, flags := splitFlags(args)
	lang := flags["lang"]
	if lang == "" && len(pos) > 0 {
		lang = pos[0]
	}
	if lang == "" {
		return fmt.Errorf("缺少 --lang（支持 python|node），用法: sandbox-packages --lang=python|node [--json]")
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/sandbox/packages", map[string]string{"lang": lang}, nil)
	if err != nil {
		return fmt.Errorf("查询已装包清单（lang=%s）失败: %w", lang, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	printData(data) // data 为字符串时原样打印
	return nil
}

// ---------------------------------------------------------------- 内部工具（sb 前缀防撞名）

// sbPrintFlat 把嵌套结构平铺成 "a.b[0].c: 值" 行（同级按 key 排序）。
func sbPrintFlat(data any) {
	for _, ln := range sbFlatten("", data) {
		fmt.Println(ln)
	}
}

// sbFlatten 递归展开：map 用点号连接父路径；标量直接成行；
// 全标量列表合并为一行；含对象的列表按 [i] 索引继续展开（保持可读性）。
func sbFlatten(prefix string, v any) []string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out = append(out, sbFlatten(path, t[k])...)
		}
		return out
	case []any:
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
				parts = append(parts, sbScalar(e))
			}
			return []string{prefix + ": [" + strings.Join(parts, ", ") + "]"}
		}
		out := make([]string, 0, len(t))
		for i, e := range t {
			out = append(out, sbFlatten(fmt.Sprintf("%s[%d]", prefix, i), e)...)
		}
		return out
	case nil:
		return []string{prefix + ": "}
	default:
		return []string{prefix + ": " + sbScalar(v)}
	}
}

// sbScalar 标量转文本（字符串原样，其余用 asString）。
func sbScalar(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return asString(v)
}
