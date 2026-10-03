// v2_fs_more.go —— 文件面补遗：整树上传（PUT /v2/fs/tree）
//
// 路由（见 V2-API.md）：PUT /v2/fs/tree?path=<远端目录>[&user=]，请求体=application/x-tar 原始字节。
//
// 实测（aiod 0.9.2，2026-10-03 在 CubeSandbox 内验证）：
//   - body 必须是**未压缩的 tar**：tar.gz 会被拒（"invalid tar archive: failed to read entire block"，
//     Content-Type 为 application/x-tar / application/gzip 都一样失败）——接口要的是 tar，不是 tar.gz；
//   - 服务端不强制请求头 Content-Type（实测 urlencoded / text/plain 也能解包），但按规范发送 application/x-tar；
//   - 成功信封 data=TreeUploadResult{path,entries,mode}：mode=rename（目标不存在，整体认领）
//     或 merge（目标已存在，逐项覆盖）；实测两种模式都能把文件落地；目标父目录必须存在，否则 404
//     "Failed to tree upload file: No such file or directory"（服务端不会自动建父目录）；
//   - 空 body → 成功、entries=0（目标不存在时相当于建了个空目录）。
//
// 实现说明：client.go 没有"自定义 Content-Type + 原始 body"的现成方法（该文件冻结、不能改），
// 故按 SPEC 在本文件内自建 fsmPUTRaw（带 SANDBOX_KEY 头、信封解析语义对齐 apiT）；
// 本文件所有辅助函数统一 fsm 前缀防撞名。
// 便利项：检测到 gzip 魔数时先在本地解压为原始 tar 再上传（stderr 提示）——服务端只认原始 tar，
// 这样用户直接给 `xxx.tar.gz` 也能工作。
//
// 输出约定：支持 --json（原样打印 data）。
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// cmdFsTreePut：把本地 tar（或 stdin）整树 PUT 到 /v2/fs/tree 解包到远端目录。
// 用法：fs-tree-put <本地 tar 文件|-> <远端目录> [--user=] [--json]
func cmdFsTreePut(args []string) error {
	usage := "fs-tree-put <本地 tar 文件|-> <远端目录> [--user=] [--json]"
	// 注意：单独的 `-` 会被 splitFlags 当作（空名）布尔开关吃掉，先扫描原始参数（同 write 命令的约定）。
	stdinMode := false
	for _, a := range args {
		if a == "-" {
			stdinMode = true
		}
	}
	pos, flags := splitFlags(args)
	var src, remote string
	if stdinMode {
		if len(pos) != 1 {
			return fmt.Errorf("stdin 模式用法: %s（`-` 只能作为第一个位置参数）", usage)
		}
		src, remote = "-", strings.TrimSpace(pos[0])
	} else {
		if err := needArgs(pos, 2, usage); err != nil {
			return err
		}
		src, remote = pos[0], strings.TrimSpace(pos[1])
	}
	if remote == "" {
		return fmt.Errorf("远端目录不能为空，用法: %s", usage)
	}

	// 1) 读入 tar 字节（`-` 表示从 stdin 读）
	var raw []byte
	if src == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("读取 stdin 失败: %w", err)
		}
		raw = b
	} else {
		b, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("读取本地文件 %s 失败: %w", src, err)
		}
		raw = b
	}

	// 2) gzip 透明解压：实测服务端只接受**原始 tar**（tar.gz 必被 422/400 拒绝），
	//    在本地解压后上传，避免用户反复踩这个坑。
	payload := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		out, err := fsmGunzip(raw)
		if err != nil {
			return fmt.Errorf("输入是 gzip 但解压失败（%s）: %w", src, err)
		}
		payload = out
		fmt.Fprintf(os.Stderr, "提示: 输入为 gzip 压缩，已在本地解压为原始 tar 再上传（服务端只接受未压缩 tar）\n")
	}
	if len(payload) == 0 {
		fmt.Fprintf(os.Stderr, "提示: 输入为 0 字节，服务端将按空 tar 处理\n")
	}

	// 3) PUT /v2/fs/tree
	c, err := mustClient()
	if err != nil {
		return err
	}
	q := map[string]string{"path": remote}
	if u := strings.TrimSpace(flags["user"]); u != "" {
		q["user"] = u
	}
	data, msg, err := fsmPUTRaw(c, "/v2/fs/tree", q, "application/x-tar", payload)
	if err != nil {
		return fmt.Errorf("整树上传到 %s 失败: %w", remote, err)
	}
	if flagBool(flags, "json") {
		printJSON(data) // 原样打印（TreeUploadResult）
		return nil
	}
	m := asMap(data)
	dest := asString(m["path"])
	if dest == "" {
		dest = remote
	}
	fmt.Printf("整树已上传: %s\n", dest)
	if v, ok := m["entries"]; ok {
		fmt.Printf("解出条目: %d\n", asInt(v, 0))
	}
	if mode := asString(m["mode"]); mode != "" {
		fmt.Printf("模式: %s\n", mode)
	}
	if msg != "" {
		fmt.Printf("服务端: %s\n", msg)
	}
	return nil
}

// ---------------------------------------------------------------- 内部工具（fsm 前缀防撞名）

// fsmPUTRaw 发送"自定义 Content-Type + 原始 body"的 PUT 请求并解析 v2 信封
// （语义对齐 client.go 的 apiT：2xx 且 success=true → (data, message)；否则 *APIError）。
// client.go 无此组合方法（冻结不可改），故在此自建；带 SANDBOX_KEY（Bearer + X-API-Key）。
func fsmPUTRaw(c *Client, path string, query map[string]string, contentType string, payload []byte) (any, string, error) {
	u := c.Base + path
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		u += "?" + q.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("X-API-Key", c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Data    any    `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, "", &APIError{Status: resp.StatusCode, Message: firstLine(raw), Body: string(raw)}
		}
		return nil, "", fmt.Errorf("响应不是 JSON 信封（HTTP %d）: %s", resp.StatusCode, firstLine(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || !env.Success {
		return nil, "", &APIError{Status: resp.StatusCode, Message: env.Message, Hint: env.Hint, Body: string(raw)}
	}
	return env.Data, env.Message, nil
}

// fsmGunzip 解压 gzip 数据（供 .tar.gz 输入在本地还原成原始 tar）。
func fsmGunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return out, nil
}
