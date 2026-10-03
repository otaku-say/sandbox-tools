// v2_mcp.go —— MCP 端点命令：POST /mcp（JSON-RPC 2.0）
//
// 用法：mcp <initialize|tools/list|tools/call|ping> [--params=JSON] [--json]
// 请求自动带上 jsonrpc="2.0" 与 id；默认只打印 result，--json 打印完整响应。
// initialize 未给 --params 时自动补最小参数（protocolVersion/capabilities/clientInfo）；
// tools/call 必须给 --params='{"name":...,"arguments":{...}}'。
//
// 实测：/mcp 返回裸 JSON-RPC 响应（不在统一信封里），所以走 apiRaw；
// tools 共 31 个（sandbox_* / browser_* 两族），返回 id 与请求一致。
package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// cmdMCP 向 /mcp 发一条 JSON-RPC 2.0 请求。
func cmdMCP(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "mcp <initialize|tools/list|tools/call|ping> [--params=JSON] [--json]"); err != nil {
		return err
	}
	method := pos[0]
	var params any
	if p, ok := flags["params"]; ok {
		v, err := jsonArg(p)
		if err != nil {
			return fmt.Errorf("--params 解析失败: %w", err)
		}
		params = v
	}
	switch method {
	case "initialize":
		if params == nil {
			params = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "sandbox-sdk-go", "version": Version},
			}
		}
	case "tools/call":
		if params == nil {
			return fmt.Errorf(`tools/call 需要 --params='{"name":...,"arguments":{...}}'`)
		}
		pm := asMap(params)
		if asString(pm["name"]) == "" {
			return fmt.Errorf(`tools/call 的 --params 必须含 name（例：--params='{"name":"sandbox_get_context","arguments":{}}'）`)
		}
	default:
		// ping / tools/list 等：params 保持用户给定（可为空）。
	}
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("POST", "/mcp", nil, req, 120*time.Second)
	if err != nil {
		return fmt.Errorf("MCP 请求失败: %w", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("MCP 响应不是 JSON: %s", firstLine(raw))
	}
	// 容错：万一响应被包进统一信封（{"success":true,"data":{...}}），剥出 data。
	if _, has := resp["jsonrpc"]; !has {
		if d := asMap(resp["data"]); d != nil {
			resp = d
		}
	}
	jsonOut := flagBool(flags, "json")
	if jsonOut {
		printJSON(resp)
	}
	if errObj := asMap(resp["error"]); errObj != nil {
		return fmt.Errorf("MCP 错误 %s: %s", asString(errObj["code"]), asString(errObj["message"]))
	}
	result, has := resp["result"]
	if !has {
		if !jsonOut {
			printJSON(resp) // 无 result 且非 --json：打印全部，避免静默
		}
		return nil
	}
	if !jsonOut {
		printJSON(result)
	}
	return nil
}
