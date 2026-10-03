package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// cmdTplLogs —— 官方 Go SDK 尚未提供模板构建日志，这里按 Python SDK 的等价端点直调：
//
//	GET {CUBESANDBOX_API_URL}/templates/{templateID}/builds/{buildID}/logs
//
// 控制面请求走公开域名，无需 CF 路径改写。
func cmdTplLogs(c interface{}, args []string) {
	_, tid, rest := splitArgs(args)
	need(tid != "" && len(rest) >= 1, "用法: tpl-logs <模板ID> <buildID>")
	api := envPick(defAPIURL, "CUBESANDBOX_API_URL", "CUBE_API_URL")
	url := strings.TrimSuffix(api, "/") + "/templates/" + tid + "/builds/" + rest[0] + "/logs"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fatal("构造请求失败: %v", err)
	}
	if k := envPick("", "CUBESANDBOX_API_KEY", "CUBE_API_KEY"); k != "" {
		req.Header.Set("X-API-Key", k)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatal("HTTP %d: %s", resp.StatusCode, string(body))
	}
	// 优先按 JSON 美化输出；不是 JSON 就原样打印
	var v any
	if json.Unmarshal(body, &v) == nil {
		printJSON(v)
		return
	}
	fmt.Println(string(body))
}
