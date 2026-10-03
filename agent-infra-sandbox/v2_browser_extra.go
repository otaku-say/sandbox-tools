// v2_browser_extra.go —— 浏览器·标签页 / Cookie / 网络日志 / 原生 CDP 面（纯 v2 API）
//
// 路由（见 V2-API.md；下列"实测"基于 aiod 0.9.2，2026-10-03 在 CubeSandbox 内验证）：
//
//	GET    /v2/browser/tabs                            —— 标签页列表；data 为 TabInfo 数组
//	                                                      实测字段 {id,title,url,type}，无 active 字段；
//	                                                      服务端把"当前活动页"排在首位（activate 会把它移到首位，
//	                                                      且 CDP 默认 tab_id 指向该页），故首行视为当前页
//	POST   /v2/browser/tabs        {url?}               —— 新建标签页；返回 TabInfo，id 即 CDP target id
//	POST   /v2/browser/tabs/{id}/activate               —— 激活标签页；返回 {id}
//	DELETE /v2/browser/tabs/{id}                        —— 关闭标签页；返回 {id}
//	GET    /v2/browser/cookies?url=&domain=             —— 读取 Cookie；data={"cookies":[...]}
//	POST   /v2/browser/cookies     {cookies:[...]}      —— 写入 Cookie；data={"count":n}
//	                                                      实测：只给 domain 不给 path 会被拒（"each cookie needs a url, or a domain and path"），
//	                                                      本命令在 domain 分支缺省时自动补 path="/"
//	GET    /v2/browser/network/requests?limit=&clear=   —— 网络请求缓冲；data={"count":n,"requests":[...]}
//	                                                      clear=true 语义 = 返回"清除前的快照"再清空（即先取后清，单次调用原子完成）
//	POST   /v2/browser/cdp         {method,params?,browser?,tab_id?} —— 原生 CDP；data=原始 result
//
// 输出约定：每个命令都支持 --json（原样打印 data）；非 JSON 模式为人类可读表格/摘要。
// 实现说明：本文件对其它代理的文件零依赖（脚手架会用桩文件编译本文件），
// 所有辅助函数带 bxe 前缀并在本文件内定义，避免撞名。
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

// ---------------------------------------------------------------- 命令实现

// cmdBrTabs 列出标签页（GET /v2/browser/tabs）。
// 表格列：当前 / tab_id / url / title；"当前"以 * 标记首行（实测：活动页排首位）。
// 用法：br-tabs [--json]
func cmdBrTabs(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/browser/tabs", nil, nil)
	if err != nil {
		return fmt.Errorf("列出标签页失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印（data 为 TabInfo 数组）
		return nil
	}
	tabs := bxeTabList(data)
	if tabs == nil {
		printData(data) // 形状未知时兜底
		return nil
	}
	if len(tabs) == 0 {
		fmt.Println("（暂无标签页）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "当前\tTAB_ID\tURL\tTITLE")
	for i, it := range tabs {
		m := asMap(it)
		if m == nil {
			continue
		}
		mark := ""
		if i == 0 {
			mark = "*" // 实测：列表首个元素 = 当前活动页
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			mark,
			asString(m["id"]),
			bxeEllipsis(asString(m["url"]), 80),
			bxeEllipsis(asString(m["title"]), 48))
	}
	_ = w.Flush()
	fmt.Println("注: * = 当前活动页（服务端将活动页排在首位）")
	return nil
}

// cmdBrTabNew 新建标签页（POST /v2/browser/tabs，body {url?}）。
// 返回的 id 即 CDP target id（后续 br-tab-use / br-tab-close 应使用它，而不是列表下标）。
// 用法：br-tab-new [--url=] [--json]（位置参数 URL 亦兼容，作为便捷写法）
func cmdBrTabNew(args []string) error {
	pos, flags := splitFlags(args)
	target := flags["url"]
	if target == "" && len(pos) > 0 {
		target = pos[0] // 宽容：允许 br-tab-new <url>
	}
	body := map[string]any{}
	if target != "" {
		body["url"] = target
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/tabs", nil, body)
	if err != nil {
		return fmt.Errorf("新建标签页失败（url=%q）: %w", target, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	tid := bxeTabIDOf(data)
	if tid == "" {
		fmt.Println("（响应未含 tab_id，原始输出如下）")
		printJSON(data)
		return nil
	}
	m := asMap(data)
	fmt.Printf("tab_id: %s\n", tid)
	fmt.Printf("url: %s\n", asString(m["url"]))
	fmt.Printf("title: %s\n", asString(m["title"]))
	if t := asString(m["type"]); t != "" {
		fmt.Printf("type: %s\n", t)
	}
	return nil
}

// cmdBrTabUse 激活标签页（POST /v2/browser/tabs/{tab_id}/activate）。
// 用法：br-tab-use <tab_id> [--json]
func cmdBrTabUse(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-tab-use <tab_id> [--json]"); err != nil {
		return err
	}
	tid := strings.TrimSpace(pos[0])
	if tid == "" {
		return fmt.Errorf("tab_id 不能为空，用法: br-tab-use <tab_id> [--json]")
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", bxeTabPath(tid, "/activate"), nil, nil)
	if err != nil {
		return fmt.Errorf("激活标签页失败（tab_id=%s）: %w", tid, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	id := bxeTabIDOf(data)
	if id == "" {
		id = tid
	}
	fmt.Printf("已激活标签页: %s（已置为当前活动页）\n", id)
	return nil
}

// cmdBrTabClose 关闭标签页（DELETE /v2/browser/tabs/{tab_id}）。
// 用法：br-tab-close <tab_id> [--json]
func cmdBrTabClose(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-tab-close <tab_id> [--json]"); err != nil {
		return err
	}
	tid := strings.TrimSpace(pos[0])
	if tid == "" {
		return fmt.Errorf("tab_id 不能为空，用法: br-tab-close <tab_id> [--json]")
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("DELETE", bxeTabPath(tid, ""), nil, nil)
	if err != nil {
		return fmt.Errorf("关闭标签页失败（tab_id=%s）: %w", tid, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	id := bxeTabIDOf(data)
	if id == "" {
		id = tid
	}
	fmt.Printf("已关闭标签页: %s\n", id)
	return nil
}

// cmdBrCookies 读取 Cookie（GET /v2/browser/cookies?url=&domain=）。
// 表格列：name / value / domain / expires（-1 显示为"会话"）。
// 用法：br-cookies [--url=] [--domain=] [--json]
func cmdBrCookies(args []string) error {
	_, flags := splitFlags(args)
	q := map[string]string{}
	if v := flags["url"]; v != "" {
		q["url"] = v
	}
	if v := flags["domain"]; v != "" {
		q["domain"] = v
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/browser/cookies", q, nil)
	if err != nil {
		return fmt.Errorf("读取 Cookie 失败: %w", err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	items := bxeCookieList(data)
	if items == nil {
		printData(data)
		return nil
	}
	if len(items) == 0 {
		fmt.Println("（无匹配的 Cookie）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVALUE\tDOMAIN\tEXPIRES")
	for _, it := range items {
		m := asMap(it)
		if m == nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			asString(m["name"]),
			bxeEllipsis(asString(m["value"]), 60),
			asString(m["domain"]),
			bxeExpires(m["expires"]))
	}
	_ = w.Flush()
	return nil
}

// cmdBrCookieSet 写入 Cookie（POST /v2/browser/cookies，body {"cookies":[CookieParam]}）。
// 需要 --name= 与 --value=，以及 --url= 或 --domain=（二选一）。
// 实测：domain 分支必须同时带 path（否则服务端 400 "each cookie needs a url, or a domain and path"），
// 因此这里在 --domain 且未给 --path 时自动补 path="/"。
// 用法：br-cookie-set --name= --value= [--url= | --domain=] [--path=] [--json]
func cmdBrCookieSet(args []string) error {
	_, flags := splitFlags(args)
	name, ok := flags["name"]
	if !ok || name == "" {
		return fmt.Errorf("缺少 --name=，用法: br-cookie-set --name= --value= [--url= | --domain=] [--path=] [--json]")
	}
	value, ok := flags["value"]
	if !ok {
		return fmt.Errorf("缺少 --value=，用法: br-cookie-set --name= --value= [--url= | --domain=] [--path=] [--json]")
	}
	ck := map[string]any{"name": name, "value": value}
	switch {
	case flags["url"] != "":
		ck["url"] = flags["url"]
		if p := flags["path"]; p != "" {
			ck["path"] = p
		}
	case flags["domain"] != "":
		ck["domain"] = flags["domain"]
		p := flags["path"]
		if p == "" {
			p = "/" // 关键分支：只给 domain 时补默认 path，否则服务端拒绝
		}
		ck["path"] = p
	default:
		return fmt.Errorf("需要 --url= 或 --domain=，用法: br-cookie-set --name= --value= [--url= | --domain=] [--path=] [--json]")
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/cookies", nil, map[string]any{"cookies": []any{ck}})
	if err != nil {
		return fmt.Errorf("写入 Cookie 失败（name=%s）: %w", name, err)
	}
	if flagBool(flags, "json") {
		printJSON(data)
		return nil
	}
	cnt := 0
	if m := asMap(data); m != nil {
		cnt = asInt(m["count"], 0)
	}
	fmt.Printf("Cookie 已写入: %s（服务端确认 %d 条）\n", name, cnt)
	return nil
}

// cmdBrNetwork 读取网络请求日志（GET /v2/browser/network/requests?limit=&clear=）。
// 列表列：method / status / url（未完成的请求 status 显示为 -）。
// --clear：服务端 clear=true 的语义就是"返回清除前的快照并清空缓冲"（单次调用原子完成，即先取后清）。
// 用法：br-network [--limit=] [--clear] [--json]
func cmdBrNetwork(args []string) error {
	_, flags := splitFlags(args)
	q := map[string]string{}
	if v, ok := flags["limit"]; ok {
		n := parseInt(v, -1)
		if n < 0 {
			return fmt.Errorf("--limit 需为非负整数（得到 %q）", v)
		}
		q["limit"] = fmt.Sprintf("%d", n)
	}
	clear := flagBool(flags, "clear")
	if clear {
		q["clear"] = "true" // 先取后清：响应即"被清掉的那批"
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/browser/network/requests", q, nil)
	if err != nil {
		return fmt.Errorf("读取网络请求日志失败: %w", err)
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
	reqs := asList(m["requests"])
	total := asInt(m["count"], len(reqs))
	if len(reqs) == 0 {
		if clear {
			fmt.Println("网络请求日志为空（已请求清空缓冲）")
		} else {
			fmt.Println("网络请求日志为空")
		}
		return nil
	}
	if total > len(reqs) {
		fmt.Printf("网络请求 %d 条（最新在前，显示前 %d 条）：\n", total, len(reqs))
	} else {
		fmt.Printf("网络请求 %d 条（最新在前）：\n", total)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "METHOD\tSTATUS\tURL")
	for _, it := range reqs {
		rm := asMap(it)
		if rm == nil {
			continue
		}
		st := "-"
		if v, ok := rm["status"]; ok && v != nil {
			st = asString(v)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			asString(rm["method"]),
			st,
			bxeEllipsis(asString(rm["url"]), 100))
	}
	_ = w.Flush()
	if clear {
		fmt.Println("（缓冲已清空）")
	}
	return nil
}

// cmdBrCDP 原生 CDP 调用（POST /v2/browser/cdp，body {method,params?,browser?,tab_id?}）。
// --params=JSON 解析后原样作为 params 传入；--browser 走浏览器级会话（Target.* / Browser.*）；
// --tab=（--tab-id= / --tab_id= 亦可）指定页面 target；默认作用于当前活动页。
// --json 原样打印 data；否则把 result 逐字段打印。
// 用法：br-cdp <CDP 方法，如 Browser.getVersion> [--params=JSON] [--browser] [--tab=] [--json]
func cmdBrCDP(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "br-cdp <CDP 方法，如 Browser.getVersion> [--params=JSON] [--browser] [--tab=] [--json]"); err != nil {
		return err
	}
	method := strings.TrimSpace(pos[0])
	if method == "" {
		return fmt.Errorf("CDP 方法不能为空，用法: br-cdp <CDP 方法，如 Browser.getVersion> [--params=JSON]")
	}
	body := map[string]any{"method": method}
	if s, ok := flags["params"]; ok {
		pv, err := jsonArg(s)
		if err != nil {
			return fmt.Errorf("--params 解析失败: %w", err)
		}
		if pv != nil {
			body["params"] = pv
		}
	}
	if flagBool(flags, "browser") {
		body["browser"] = true // 浏览器级会话（Target.* / Browser.* 方法需要）
	}
	if tid := bxeTabID(flags); tid != "" {
		body["tab_id"] = tid
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/browser/cdp", nil, body)
	if err != nil {
		return fmt.Errorf("CDP 调用失败（method=%s）: %w", method, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印 result
		return nil
	}
	bxePrintResult(data)
	return nil
}

// ---------------------------------------------------------------- 内部工具（bxe 前缀防撞名）

// bxeTabID 依次从 --tab= / --tab-id= / --tab_id= 取标签页 id（br-cdp 可选参数）。
func bxeTabID(flags map[string]string) string {
	for _, k := range []string{"tab", "tab-id", "tab_id"} {
		if v := strings.TrimSpace(flags[k]); v != "" {
			return v
		}
	}
	return ""
}

// bxeTabPath 拼接标签页子路由并对 tab_id 做路径转义。
func bxeTabPath(tabID, suffix string) string {
	return "/v2/browser/tabs/" + url.PathEscape(tabID) + suffix
}

// bxeTabIDOf 从响应 data 里取 tab 的 CDP target id（兼容 id / tab_id / target_id 命名）。
func bxeTabIDOf(data any) string {
	if m := asMap(data); m != nil {
		for _, k := range []string{"id", "tab_id", "target_id"} {
			if v := asString(m[k]); v != "" {
				return v
			}
		}
	}
	if s, ok := data.(string); ok {
		return s
	}
	return ""
}

// bxeTabList 提取标签页数组：data 为数组时直接返回；为对象时取 tabs 字段。
// 返回 nil 表示形状未知（调用方走兜底输出）。
func bxeTabList(data any) []any {
	if l := asList(data); l != nil {
		return l
	}
	if m := asMap(data); m != nil {
		if l := asList(m["tabs"]); l != nil {
			return l
		}
	}
	return nil
}

// bxeCookieList 提取 Cookie 数组（实测 data={"cookies":[...]}）。
func bxeCookieList(data any) []any {
	if m := asMap(data); m != nil {
		if l := asList(m["cookies"]); l != nil {
			return l
		}
	}
	return asList(data)
}

// bxeExpires 渲染 Cookie 过期时间：负数（-1）为会话 Cookie，其余原样打印。
func bxeExpires(v any) string {
	n := asInt(v, -1)
	if n < 0 {
		return "会话"
	}
	return fmt.Sprintf("%d", n)
}

// bxeEllipsis 按 rune 截断超长文本（尾部加 …）。
func bxeEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// bxeSortedKeys 返回 map 的 key 排序列表。
func bxeSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bxePrintResult 打印 CDP result：对象逐字段输出（标量直出、嵌套走紧凑 JSON）；
// 其余类型（数组/标量/null）交给 printData。
func bxePrintResult(data any) {
	if m := asMap(data); m != nil && len(m) > 0 {
		for _, k := range bxeSortedKeys(m) {
			switch v := m[k].(type) {
			case map[string]any, []any:
				b, err := json.Marshal(v)
				if err != nil {
					fmt.Printf("%s: %s\n", k, asString(v))
					continue
				}
				fmt.Printf("%s: %s\n", k, string(b))
			default:
				fmt.Printf("%s: %s\n", k, asString(m[k]))
			}
		}
		return
	}
	printData(data)
}
