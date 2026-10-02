package main

import "github.com/agent-infra/sandbox-sdk-go/client"

// 命名空间命令表：`sandbox-sdk-go <ns> <action> [参数]`
// 覆盖 agent-infra/sandbox Go SDK 的全部 20 个子客户端（132 个方法）。
//
//	file    file  list|read|write|replace|search|find|grep|glob|upload|download|str-replace|watch-*
//	code    code  run|info
//	jupyter jupyter run|info|ls|new|rm|rm-all
//	nodejs  nodejs run|info|ls|new|get|rm|update
//	util    util  markdown
//	browser browser info|config|restart|screenshot|action|pac
//	page    page  navigate|back|forward|reload|click|fill|type|press|hotkey|hover|select|check|uncheck|
//	              upload|fill-form|scroll|scroll-to|scroll-to-element|screenshot|get-html|get-text|get-markdown|
//	              elements|console|export-console|evaluate|find-text|wait|record
//	tabs    tabs  ls|new|close|activate
//	cookies cookies ls|set|clear
//	state   state save|load
//	net     net   headers|scoped-headers|route-add|route-rm|requests|har
//	captcha captcha detect|wait
//	mcp     mcp   servers|tools|call
//	skills  skills ls|content|register|rm|clear
//	hooks   hooks ls|add|rm
//	proxy   proxy ls|add|rm|excludes|exclude-add|exclude-rm|upstream|upstream-set|upstream-rm|health|diagnose
//	display display record
//	auth    auth  ticket|verify
//	ctxinfo ctxinfo context|py-packages|node-packages|hooks
var namespaces = map[string]func(*client.Client, []string){
	"file":    cmdFile,
	"code":    cmdCodeNS,
	"jupyter": cmdJupyter,
	"nodejs":  cmdNodejs,
	"util":    cmdUtil,
	"browser": cmdBrowser,
	"page":    cmdPage,
	"tabs":    cmdTabs,
	"cookies": cmdCookies,
	"state":   cmdState,
	"net":     cmdNet,
	"captcha": cmdCaptcha,
	"mcp":     cmdMcp,
	"skills":  cmdSkills,
	"hooks":   cmdHooks,
	"proxy":   cmdProxy,
	"display": cmdDisplay,
	"auth":    cmdAuth,
	"ctxinfo": cmdCtxInfo,
}
