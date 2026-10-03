// client.go —— v2 纯 HTTP 客户端核心（冻结文件，子代理请勿修改）
//
// 约定：
//   - 只依赖标准库；
//   - 所有请求走 Client.api / apiT / apiRaw / apiMultipart；
//   - 响应统一信封 {success,message,data,hint}，api* 返回的是 **data**；
//   - 非 2xx 或 success=false → 返回 *APIError（带 status/message/hint）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version 由构建期注入（-ldflags "-X main.Version=..."），否则用默认值。
var Version = "0.1.0-dev"

type Client struct {
	Base string
	Key  string
	HTTP *http.Client
}

// APIError 携带服务端返回的状态码与信封信息。
type APIError struct {
	Status  int
	Message string
	Hint    string
	Body    string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	if e.Hint != "" {
		msg += "（hint: " + e.Hint + "）"
	}
	return msg
}

// NewClient 从环境变量读取连接信息：
//
//	SANDBOX_BASE  必填，如 https://<proxy-host>/sandbox/<sandboxID>/8080
//	SANDBOX_KEY   可选，非空时作为 Authorization: Bearer 与 X-API-Key 发送
func NewClient() (*Client, error) {
	base := strings.TrimSpace(os.Getenv("SANDBOX_BASE"))
	if base == "" {
		return nil, fmt.Errorf("SANDBOX_BASE 未设置（例：https://<proxy-host>/sandbox/<sandboxID>/8080）")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	return &Client{
		Base: strings.TrimRight(base, "/"),
		Key:  strings.TrimSpace(os.Getenv("SANDBOX_KEY")),
		HTTP: &http.Client{},
	}, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, query map[string]string, body any, raw bool) (*http.Request, error) {
	u := c.Base + path
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rdr = bytes.NewReader(b)
		default:
			buf, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("请求体序列化失败: %w", err)
			}
			rdr = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if _, ok := body.([]byte); !ok {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if !raw {
		req.Header.Set("Accept", "application/json")
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("X-API-Key", c.Key)
	}
	return req, nil
}

// doRaw 执行请求并返回原始响应体（用于截图 / 下载等二进制接口）。
func (c *Client) doRaw(ctx context.Context, method, path string, query map[string]string, body any) ([]byte, error) {
	req, err := c.newRequest(ctx, method, path, query, body, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Message: firstLine(data), Body: string(data)}
	}
	return data, nil
}

// apiRaw 取原始响应（二进制安全）。
func (c *Client) apiRaw(method, path string, query map[string]string, body any, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.doRaw(ctx, method, path, query, body)
}

// apiT 与 api 相同，但可指定超时（0 = 默认 300s）。
func (c *Client) apiT(method, path string, query map[string]string, body any, timeout time.Duration) (any, error) {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, query, body, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
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
			return nil, &APIError{Status: resp.StatusCode, Message: firstLine(raw), Body: string(raw)}
		}
		return nil, fmt.Errorf("响应不是 JSON 信封（HTTP %d）: %s", resp.StatusCode, firstLine(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Message: env.Message, Hint: env.Hint, Body: string(raw)}
	}
	if !env.Success {
		return nil, &APIError{Status: resp.StatusCode, Message: env.Message, Hint: env.Hint, Body: string(raw)}
	}
	return env.Data, nil
}

// api 默认超时 300s。
func (c *Client) api(method, path string, query map[string]string, body any) (any, error) {
	return c.apiT(method, path, query, body, 0)
}

// apiMultipart 走 multipart/form-data 上传（/v2/fs/upload 用，字段名 file）。
func (c *Client) apiMultipart(path, field, filename string, content []byte) (any, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("X-API-Key", c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("上传响应解析失败（HTTP %d）: %s", resp.StatusCode, firstLine(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || !env.Success {
		return nil, &APIError{Status: resp.StatusCode, Message: env.Message, Hint: env.Hint, Body: string(raw)}
	}
	return env.Data, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ---------------------------------------------------------------- 小工具

// splitFlags 解析参数：支持 --k=v / --flag（布尔）/ -k=v，其余进 pos。
// 约定：需要取值的参数一律用 `--key=value`（避免 `--key value` 的歧义）。
func splitFlags(args []string) (pos []string, flags map[string]string) {
	flags = map[string]string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			trimmed := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
				flags[trimmed[:eq]] = trimmed[eq+1:]
			} else {
				flags[trimmed] = "true"
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos, flags
}

func flagBool(flags map[string]string, key string) bool {
	v, ok := flags[key]
	return ok && v != "false" && v != "0"
}

// jsonArg 解析 --json='{...}' 之类的内联 JSON。
func jsonArg(s string) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return v, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func asInt(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		var n int
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func sMap(m map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = asString(v)
	}
	return out
}

func parseInt(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return def
}
