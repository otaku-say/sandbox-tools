// cmd_browser_extra.go — browser 附属命名空间的 CLI 封装（共 17 个方法）：
//
//	tabs    ls|new|close|activate                                  （browsertabs，4）
//	cookies ls|set|clear                                           （browsercookies，3）
//	state   save|load                                              （browserstate，2）
//	net     headers|scoped-headers|route-add|route-rm|requests|har （browsernetwork，6）
//	captcha detect|wait                                            （browsercaptcha，2）
//
// 约定与 cmd_browser.go 相同：--key=value 选项、位置参数优先、printJSON 输出。
// 字段/类型逐字对齐 API-REFERENCE.md §3.10–§3.14。
package main

import (
	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

const tabsUsageText = `用法: sandbox-sdk-go tabs <action> [参数]

  ls                       列出所有标签页
  new [url]                新建标签页（可带初始 URL）
  close <index>            关闭指定标签页
  activate <index>         激活（切到前台）指定标签页
`

const cookiesUsageText = `用法: sandbox-sdk-go cookies <action> [参数]

  ls [urls]                读取 Cookie（可按 URL 过滤，逗号分隔）
  set '<json>'             设置 Cookie（JSON 数组或单个对象）；
                           或 --name=<名> [--value= --domain= --path= --url= --same-site=]
                              [--secure] [--http-only] [--expires=秒]
  clear                    清空全部 Cookie
`

const stateUsageText = `用法: sandbox-sdk-go state <action> [参数]

  save <path>   保存浏览器状态（cookies/localStorage 等）到文件
  load <path>   从文件加载浏览器状态
`

const netUsageText = `用法: sandbox-sdk-go net <action> [参数]

  headers k=v [k2=v2 ...]          设置全局请求头（或 --headers=k=v,k2=v2、--json='{"k":"v"}'）
  scoped-headers <origin> k=v ...  按域名设置请求头
  route-add <url-pattern> [--abort] [--status=200] [--body=..] [--content-type=..] [--headers=k=v,...]
                                   添加路由拦截（mock 响应或直接 abort）
  route-rm <url-pattern>           移除路由拦截
  requests [--filter=] [--limit=]  查看已记录的请求
  har <save_path>                  导出 HAR 文件
`

const captchaUsageText = `用法: sandbox-sdk-go captcha <action> [参数]

  detect                                检测当前页面是否有验证码
  wait [秒] [--timeout=秒] [--poll-interval=秒]   等待验证码被解决
`

// ---------- browsertabs（4） ----------

// cmdTabs 实现 tabs：ls / new / close / activate。
func cmdTabs(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, tabsUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "ls":
		resp, err := c.BrowserTabs.List(ctx)
		if err != nil {
			fatal("tabs ls: %v", err)
		}
		printJSON(resp)
	case "new":
		req := &sandboxsdkgo.CreatePageRequest{Url: browserOptStr(browserPick(pos, 0, flags, "url"))}
		resp, err := c.BrowserTabs.Create(ctx, req)
		if err != nil {
			fatal("tabs new: %v", err)
		}
		printJSON(resp)
	case "close":
		index := browserIntArg(pos, 0, flags, "index", "用法: sandbox-sdk-go tabs close <index>")
		resp, err := c.BrowserTabs.Close(ctx, index)
		if err != nil {
			fatal("tabs close: %v", err)
		}
		printJSON(resp)
	case "activate":
		index := browserIntArg(pos, 0, flags, "index", "用法: sandbox-sdk-go tabs activate <index>")
		resp, err := c.BrowserTabs.Activate(ctx, index)
		if err != nil {
			fatal("tabs activate: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, tabsUsageText)
	}
}

// ---------- browsercookies（3） ----------

// cmdCookies 实现 cookies：ls / set / clear。
func cmdCookies(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, cookiesUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "ls":
		req := &sandboxsdkgo.BrowserCookiesGetCookiesRequest{Urls: browserOptStr(browserPick(pos, 0, flags, "urls"))}
		resp, err := c.BrowserCookies.GetCookies(ctx, req)
		if err != nil {
			fatal("cookies ls: %v", err)
		}
		printJSON(resp)
	case "set":
		req := &sandboxsdkgo.CookieSetRequest{Cookies: browserCookies(flags, pos)}
		resp, err := c.BrowserCookies.SetCookies(ctx, req)
		if err != nil {
			fatal("cookies set: %v", err)
		}
		printJSON(resp)
	case "clear":
		resp, err := c.BrowserCookies.ClearCookies(ctx)
		if err != nil {
			fatal("cookies clear: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, cookiesUsageText)
	}
}

// browserCookies 组装 cookies set 的请求体：优先 --json/位置参数 JSON；否则按单条字段拼装。
func browserCookies(flags map[string]string, pos []string) []map[string]any {
	raw := fget(flags, "json", "")
	if raw == "" && len(pos) > 0 {
		raw = pos[0]
	}
	if raw != "" {
		items := browserJSONItems(raw)
		if len(items) == 0 {
			fatal("cookies set: JSON 为空")
		}
		return items
	}
	name := fget(flags, "name", "")
	if name == "" {
		fatal("用法: sandbox-sdk-go cookies set '<json>' 或 --name=<名> [--value= --domain= --path= --url= --secure --http-only --same-site= --expires=]")
	}
	ck := map[string]any{"name": name, "value": fget(flags, "value", "")}
	if v := fget(flags, "domain", ""); v != "" {
		ck["domain"] = v
	}
	if v := fget(flags, "path", ""); v != "" {
		ck["path"] = v
	}
	if v := fget(flags, "url", ""); v != "" {
		ck["url"] = v
	}
	if v := fget(flags, "same-site", ""); v != "" {
		ck["sameSite"] = v
	}
	if b := fbool(flags, "secure"); b != nil {
		ck["secure"] = *b
	}
	if b := fbool(flags, "http-only"); b != nil {
		ck["httpOnly"] = *b
	}
	if n := fint(flags, "expires"); n != nil {
		ck["expires"] = *n
	}
	return []map[string]any{ck}
}

// ---------- browserstate（2） ----------

// cmdState 实现 state：save / load。
func cmdState(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, stateUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "save":
		path := browserNeed(pos, 0, flags, "path", "用法: sandbox-sdk-go state save <path>")
		resp, err := c.BrowserState.Save(ctx, &sandboxsdkgo.StateSaveRequest{Path: path})
		if err != nil {
			fatal("state save: %v", err)
		}
		printJSON(resp)
	case "load":
		path := browserNeed(pos, 0, flags, "path", "用法: sandbox-sdk-go state load <path>")
		resp, err := c.BrowserState.Load(ctx, &sandboxsdkgo.StateLoadRequest{Path: path})
		if err != nil {
			fatal("state load: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, stateUsageText)
	}
}

// ---------- browsernetwork（6） ----------

// cmdNet 实现 net：headers / scoped-headers / route-add / route-rm / requests / har。
func cmdNet(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, netUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "headers":
		usage := "用法: sandbox-sdk-go net headers k=v [k2=v2 ...]（或 --headers=k=v,k2=v2、--json='{\"k\":\"v\"}'）"
		headers := browserKVFrom(pos, flags, "headers")
		if headers == nil {
			fatal("%s", usage)
		}
		resp, err := c.BrowserNetwork.SetHeaders(ctx, &sandboxsdkgo.HeadersRequest{Headers: headers})
		if err != nil {
			fatal("net headers: %v", err)
		}
		printJSON(resp)
	case "scoped-headers":
		usage := "用法: sandbox-sdk-go net scoped-headers <origin> k=v [k2=v2 ...]"
		origin := browserNeed(pos, 0, flags, "origin", usage)
		headers := browserKVFrom(pos[1:], flags, "headers")
		if headers == nil {
			fatal("%s", usage)
		}
		resp, err := c.BrowserNetwork.SetScopedHeaders(ctx, &sandboxsdkgo.ScopedHeadersRequest{Origin: origin, Headers: headers})
		if err != nil {
			fatal("net scoped-headers: %v", err)
		}
		printJSON(resp)
	case "route-add":
		usage := "用法: sandbox-sdk-go net route-add <url-pattern> [--abort] [--status=200] [--body=..] [--content-type=..] [--headers=k=v,...]"
		pattern := browserNeed(pos, 0, flags, "url-pattern", usage)
		req := &sandboxsdkgo.NetworkRouteRequest{UrlPattern: pattern, Abort: fbool(flags, "abort")}
		route := &sandboxsdkgo.RouteResponseModel{}
		hasResponse := false
		if st := fint(flags, "status"); st != nil {
			route.Status = st
			hasResponse = true
		}
		if body := fget(flags, "body", ""); body != "" {
			route.Body = sandboxsdkgo.String(body)
			hasResponse = true
		}
		if ct := fget(flags, "content-type", ""); ct != "" {
			route.ContentType = sandboxsdkgo.String(ct)
			hasResponse = true
		}
		if hs := browserKVFrom(pos[1:], flags, "headers"); hs != nil {
			route.Headers = hs
			hasResponse = true
		}
		if hasResponse {
			req.Response = route
		}
		resp, err := c.BrowserNetwork.AddRoute(ctx, req)
		if err != nil {
			fatal("net route-add: %v", err)
		}
		printJSON(resp)
	case "route-rm":
		pattern := browserNeed(pos, 0, flags, "url-pattern", "用法: sandbox-sdk-go net route-rm <url-pattern>")
		resp, err := c.BrowserNetwork.RemoveRoute(ctx, &sandboxsdkgo.NetworkRouteRemoveRequest{UrlPattern: pattern})
		if err != nil {
			fatal("net route-rm: %v", err)
		}
		printJSON(resp)
	case "requests":
		req := &sandboxsdkgo.BrowserNetworkGetRequestsRequest{
			Filter: browserOptStr(fget(flags, "filter", "")),
			Limit:  fint(flags, "limit"),
		}
		resp, err := c.BrowserNetwork.GetRequests(ctx, req)
		if err != nil {
			fatal("net requests: %v", err)
		}
		printJSON(resp)
	case "har":
		path := browserNeed(pos, 0, flags, "save-path", "用法: sandbox-sdk-go net har <save_path>")
		resp, err := c.BrowserNetwork.ExportHar(ctx, &sandboxsdkgo.ExportHarRequest{SavePath: path})
		if err != nil {
			fatal("net har: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, netUsageText)
	}
}

// ---------- browsercaptcha（2） ----------

// cmdCaptcha 实现 captcha：detect / wait。
func cmdCaptcha(c *client.Client, args []string) {
	if len(args) == 0 {
		browserUsageOut(0, captchaUsageText)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])
	switch action {
	case "detect":
		resp, err := c.BrowserCaptcha.Detect(ctx)
		if err != nil {
			fatal("captcha detect: %v", err)
		}
		printJSON(resp)
	case "wait":
		req := &sandboxsdkgo.CaptchaWaitRequest{
			Timeout:      browserOptFloat(pos, 0, flags, "timeout"),
			PollInterval: ffloat(flags, "poll-interval"),
		}
		resp, err := c.BrowserCaptcha.Wait(ctx, req)
		if err != nil {
			fatal("captcha wait: %v", err)
		}
		printJSON(resp)
	default:
		browserUsageOut(2, captchaUsageText)
	}
}
