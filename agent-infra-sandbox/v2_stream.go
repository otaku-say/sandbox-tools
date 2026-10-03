// v2_stream.go —— 流式面：PTY WebSocket 附着（pty-ws / pty-ws-anon）与 watch SSE（watch-events）
//
// 实现约束（见 SPEC.md）：只用标准库；WebSocket 握手与帧编解码全部自己实现
// （net / crypto/tls / crypto/sha1 / encoding/base64 / bufio / encoding/binary）。
//
// 实测备注（aio-daemon 0.9.2，Rust axum 后端；在沙箱 127.0.0.1:8080 真连验证）：
//   - 握手：标准 RFC6455。101 + Sec-WebSocket-Accept 校验通过；HTTPS 走 tls.Dial，
//     经 cubesandbox-proxy（Cloudflare）也能 101（代理支持 WebSocket 升级）。
//   - protocol=json（默认）：双向都是 text 帧。服务端消息为 JSON 信封：
//     {"type":"ready","backend":"native","resumed":false,"session_id":"...","transport":"json"}
//     {"type":"restore_output","data":"<附着时回放的终端历史>"}
//     {"type":"terminal_restored","session_id":"..."}
//     {"type":"output","data":"<实时终端输出>"}
//     客户端输入必须发 {"type":"input","data":"<文本/按键>"}；实测发其它形态的
//     JSON 或裸文本帧会被服务端当作"直接键入"整段送进 PTY（不是报错）。
//   - protocol=binary：服务端控制消息（ready/terminal_restored）仍是 text 帧 JSON，
//     终端数据走 binary 帧原始字节；客户端输入用 binary 帧原始字节（实测回显正常）。
//   - 附着（含首次）时服务端先回放会话历史（restore_output / binary 回放数据），
//     再发 terminal_restored。pty-ws-anon 连接即用、断开即销毁（连接级 WebShell）。
//   - watch-events（SSE）实测流格式：
//     event: watch_started\ndata: {"watcher_id":"..."}\n\n
//     id: <watcher_id>:<seq>\nevent: file_change\ndata: {seq,type,path,...}\n\n
//     注意 id 行在 event 行之前（解析按块处理，不依赖行序）；无心跳注释行。
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------- WebSocket 基础

// wsMagicGUID 是 RFC6455 计算 Sec-WebSocket-Accept 用的固定 GUID。
const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcode（RFC6455 §5.2）。
const (
	wsOpContinuation byte = 0x0
	wsOpText         byte = 0x1
	wsOpBinary       byte = 0x2
	wsOpClose        byte = 0x8
	wsOpPing         byte = 0x9
	wsOpPong         byte = 0xA
)

// wsMaxFrameSize 单帧/单消息大小上限（防止异常数据打爆内存）。
const wsMaxFrameSize = 32 << 20

// wsEndpoint 描述一次 WebSocket 连接所需的地址信息（全部从 SANDBOX_BASE 推导）。
type wsEndpoint struct {
	hostPort   string // TCP 拨号地址 host:port
	hostHeader string // HTTP Host 头（含非默认端口）
	reqURI     string // 请求行 URI：base path + api path + query
	useTLS     bool   // https/wss → true
	serverName string // TLS SNI 与证书校验名
}

// wsEndpointFromBase 从客户端 Base（如 https://host/sandbox/<id>/8080）推导连接目标：
// scheme→是否 TLS、host/port→拨号地址、path 前缀→拼进请求行。
func wsEndpointFromBase(base, apiPath string, query map[string]string) (*wsEndpoint, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("SANDBOX_BASE 不是合法 URL: %w", err)
	}
	var useTLS bool
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		useTLS = true
	case "http", "ws", "":
		useTLS = false
	default:
		return nil, fmt.Errorf("不支持的 SANDBOX_BASE scheme: %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("SANDBOX_BASE 缺少主机名: %q", base)
	}
	port := u.Port()
	if port == "" {
		if useTLS {
			port = "443"
		} else {
			port = "80"
		}
	}
	reqURI := strings.TrimRight(u.Path, "/") + apiPath
	if reqURI == "" {
		reqURI = "/"
	}
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			if strings.TrimSpace(v) != "" {
				q.Set(k, v)
			}
		}
		if enc := q.Encode(); enc != "" {
			reqURI += "?" + enc
		}
	}
	return &wsEndpoint{
		hostPort:   net.JoinHostPort(host, port),
		hostHeader: u.Host,
		reqURI:     reqURI,
		useTLS:     useTLS,
		serverName: host,
	}, nil
}

// wsAcceptKey 计算期望的 Sec-WebSocket-Accept：base64(sha1(key + GUID))。
func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsMagicGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsHeaderContains 判断头字段值（逗号分隔的 token 列表）是否包含指定 token（忽略大小写）。
func wsHeaderContains(headers map[string]string, name, token string) bool {
	for _, part := range strings.Split(headers[name], ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// wsReadLine 读取一行（去掉行尾 CRLF/LF）；头部阶段行过长直接算错误。
func wsReadLine(br *bufio.Reader) (string, error) {
	s, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// wsReadHTTPHead 读取 HTTP 响应头：状态行 + 头字段（头名小写，重复头合并）。
func wsReadHTTPHead(br *bufio.Reader) (int, map[string]string, error) {
	line, err := wsReadLine(br)
	if err != nil {
		return 0, nil, err
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, nil, fmt.Errorf("非法状态行: %q", wsClip(line, 200))
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, nil, fmt.Errorf("非法状态码: %q", parts[1])
	}
	headers := map[string]string{}
	for {
		line, err := wsReadLine(br)
		if err != nil {
			return 0, nil, err
		}
		if line == "" {
			break
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		if old, ok := headers[name]; ok {
			headers[name] = old + ", " + val
		} else {
			headers[name] = val
		}
	}
	return status, headers, nil
}

// wsClip 截断过长文本（错误信息用）。
func wsClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// wsBestEffortBody 握手失败（非 101）时尽力读一段响应体做诊断，最多等 2 秒。
func wsBestEffortBody(br *bufio.Reader, conn net.Conn) string {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 2048)
	n, _ := br.Read(buf)
	if n <= 0 {
		return ""
	}
	return strings.TrimSpace(string(buf[:n]))
}

// wsConn 是一条已完成握手的 WebSocket 连接（读写并发安全：写加锁）。
type wsConn struct {
	conn   net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex // 串行化写帧（stdin 协程 / 读循环回 pong 都可能写）
	closed bool
}

// wsDial 完成 TCP/TLS 拨号 + RFC6455 握手，校验 101 与 Sec-WebSocket-Accept。
func wsDial(c *Client, apiPath string, query map[string]string) (*wsConn, error) {
	ep, err := wsEndpointFromBase(c.Base, apiPath, query)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if ep.useTLS {
		conn, err = tls.DialWithDialer(&d, "tcp", ep.hostPort, &tls.Config{ServerName: ep.serverName})
	} else {
		conn, err = d.Dial("tcp", ep.hostPort)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", ep.hostPort, err)
	}
	// 握手阶段限时 15s，成功后清除期限走阻塞读写。
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		conn.Close()
		return nil, fmt.Errorf("生成 Sec-WebSocket-Key 失败: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)

	var sb strings.Builder
	fmt.Fprintf(&sb, "GET %s HTTP/1.1\r\n", ep.reqURI)
	fmt.Fprintf(&sb, "Host: %s\r\n", ep.hostHeader)
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&sb, "Sec-WebSocket-Key: %s\r\n", key)
	sb.WriteString("Sec-WebSocket-Version: 13\r\n")
	if c.Key != "" { // 与 client.go 一致：网关/服务端要求鉴权时带上
		fmt.Fprintf(&sb, "Authorization: Bearer %s\r\n", c.Key)
		fmt.Fprintf(&sb, "X-API-Key: %s\r\n", c.Key)
	}
	sb.WriteString("\r\n")
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 WebSocket 握手请求失败: %w", err)
	}

	br := bufio.NewReaderSize(conn, 64*1024)
	status, headers, err := wsReadHTTPHead(br)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 WebSocket 握手响应失败: %w", err)
	}
	if status != 101 {
		body := wsBestEffortBody(br, conn)
		conn.Close()
		msg := fmt.Sprintf("WebSocket 握手被拒绝: HTTP %d", status)
		if body != "" {
			msg += "，服务端响应: " + wsClip(firstLine([]byte(body)), 300)
		}
		return nil, errors.New(msg)
	}
	if !wsHeaderContains(headers, "upgrade", "websocket") {
		conn.Close()
		return nil, fmt.Errorf("握手响应缺少 Upgrade: websocket（Upgrade=%q）", headers["upgrade"])
	}
	if !wsHeaderContains(headers, "connection", "upgrade") {
		conn.Close()
		return nil, fmt.Errorf("握手响应缺少 Connection: upgrade（Connection=%q）", headers["connection"])
	}
	if got := headers["sec-websocket-accept"]; got != wsAcceptKey(key) {
		conn.Close()
		return nil, fmt.Errorf("Sec-WebSocket-Accept 校验失败: 收到 %q", wsClip(got, 120))
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

// readFrame 读取一个帧，返回 (fin, opcode, payload)。
// 客户端方向本应收到未掩码帧；为健壮性兼容被掩码的帧（自动解掩码）。
func (w *wsConn) readFrame() (bool, byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(w.br, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin := head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		return false, 0, nil, fmt.Errorf("协议错误: RSV 位非 0（首字节 %#x）", head[0])
	}
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > wsMaxFrameSize {
		return false, 0, nil, fmt.Errorf("帧过大（%d 字节，上限 %d）", length, wsMaxFrameSize)
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(w.br, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i&3]
		}
	}
	// 控制帧必须 FIN=1 且 ≤125 字节（RFC6455 §5.5）。
	if opcode >= 0x8 && (!fin || len(payload) > 125) {
		return false, 0, nil, fmt.Errorf("协议错误: 非法控制帧（fin=%v len=%d）", fin, len(payload))
	}
	return fin, opcode, payload, nil
}

// writeFrame 写一个客户端帧（RFC6455 要求客户端发出的帧必须掩码）。
func (w *wsConn) writeFrame(opcode byte, fin bool, payload []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if w.closed {
		return fmt.Errorf("连接已关闭")
	}
	var head [10]byte
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	head[0] = b0
	n := 2
	switch size := len(payload); {
	case size < 126:
		head[1] = 0x80 | byte(size)
	case size <= 0xffff:
		head[1] = 0x80 | 126
		binary.BigEndian.PutUint16(head[2:4], uint16(size))
		n = 4
	default:
		head[1] = 0x80 | 127
		binary.BigEndian.PutUint64(head[2:10], uint64(size))
		n = 10
	}
	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	buf := make([]byte, 0, n+4+len(payload))
	buf = append(buf, head[:n]...)
	buf = append(buf, maskKey[:]...)
	off := len(buf)
	buf = append(buf, payload...)
	for i := range payload {
		buf[off+i] ^= maskKey[i&3]
	}
	_, err := w.conn.Write(buf)
	return err
}

// sendText 发一个 text 帧。
func (w *wsConn) sendText(s string) error { return w.writeFrame(wsOpText, true, []byte(s)) }

// sendBinary 发一个 binary 帧。
func (w *wsConn) sendBinary(b []byte) error { return w.writeFrame(wsOpBinary, true, b) }

// shutdown 发送 close 帧并关闭底层连接（幂等；忽略关闭帧本身的写错误）。
func (w *wsConn) shutdown(code uint16, reason string) {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if w.closed {
		return
	}
	if len(reason) > 123 { // close 帧负载上限 125（2 字节状态码 + ≤123 字节原因）
		reason = reason[:123]
	}
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], code)
	copy(payload[2:], reason)
	_ = w.sendCloseFrameLocked(payload)
	w.closed = true
	_ = w.conn.Close()
}

// sendCloseFrameLocked 在已持锁的前提下构造并写出一个 close 帧。
func (w *wsConn) sendCloseFrameLocked(payload []byte) error {
	var head [10]byte
	head[0] = 0x80 | wsOpClose
	n := 2
	switch size := len(payload); {
	case size < 126:
		head[1] = 0x80 | byte(size)
	case size <= 0xffff:
		head[1] = 0x80 | 126
		binary.BigEndian.PutUint16(head[2:4], uint16(size))
		n = 4
	default:
		return fmt.Errorf("close 帧过大")
	}
	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	buf := make([]byte, 0, n+4+len(payload))
	buf = append(buf, head[:n]...)
	buf = append(buf, maskKey[:]...)
	off := len(buf)
	buf = append(buf, payload...)
	for i := range payload {
		buf[off+i] ^= maskKey[i&3]
	}
	_, err := w.conn.Write(buf)
	return err
}

// close 直接断开（不体面地放弃关闭握手；用于错误清理路径）。
func (w *wsConn) close() { w.shutdown(1000, "") }

// wsParseClose 解析 close 帧 payload：无负载 = 1005（无状态码）。
func wsParseClose(payload []byte) (uint16, string) {
	if len(payload) < 2 {
		return 1005, ""
	}
	code := binary.BigEndian.Uint16(payload[:2])
	reason := string(payload[2:])
	return code, wsClip(reason, 123)
}

// wsUTF8Split 把数据切成 (可安全打包的完整 UTF-8 前缀, 末尾不完整序列)：
// json 模式把 stdin 字节塞进 JSON 字符串，若是多字节字符被 chunk 切开，
// json.Marshal 会把半个字符变成 U+FFFD；这里把不完整的尾巴留到下一 chunk。
func wsUTF8Split(p []byte) (head, tail []byte) {
	if len(p) == 0 {
		return p, nil
	}
	i := len(p) - 1
	for j := 0; j < 4 && i >= 0; j++ {
		b := p[i]
		if b < 0x80 { // ASCII：边界即 i+1
			return p[:i+1], p[i+1:]
		}
		if b&0xC0 == 0xC0 { // 多字节序列的起始字节
			need := 2
			switch {
			case b&0xF0 == 0xE0:
				need = 3
			case b&0xF8 == 0xF0:
				need = 4
			}
			if i+need <= len(p) {
				return p, nil // 序列完整
			}
			return p[:i], p[i:] // 序列被截断，尾巴留到下次
		}
		i-- // 0x80-0xBF：延续字节，继续往前找起始字节
	}
	return p, nil // 找不到边界（非法 UTF-8）：原样发给服务端
}

// ---------------------------------------------------------------- PTY WebSocket 交互

// streamPtyOpts 是 pty-ws / pty-ws-anon 的运行参数。
type streamPtyOpts struct {
	Protocol string // json | binary
	JSONDump bool   // --json：收到的文本帧原样打印（不做解析；二进制帧仍原样落屏）
}

// streamProtocol 解析并校验 --protocol=（缺省 json）。
func streamProtocol(flags map[string]string) (string, error) {
	p := strings.TrimSpace(flags["protocol"])
	if p == "" {
		p = "json"
	}
	if p != "json" && p != "binary" {
		return "", fmt.Errorf("--protocol 只支持 json 或 binary（给的是 %q）", p)
	}
	return p, nil
}

// streamSendInput 把一段 stdin 数据发给服务端：
// json 模式包成 {"type":"input","data":...} 文本帧；binary 模式直接发原始字节。
func streamSendInput(w *wsConn, opts streamPtyOpts, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if opts.Protocol == "json" {
		msg, err := json.Marshal(map[string]string{"type": "input", "data": string(data)})
		if err != nil {
			return err
		}
		return w.sendText(string(msg))
	}
	return w.sendBinary(data)
}

// streamDeliverFrame 处理一个完整消息（text/binary 或分片重组后的结果）。
// 返回值 srvErr：服务端 error 消息的文本（空 = 无错误），用于决定最终退出码；
// werr：写 stdout 失败等本地错误（非 nil 时主循环直接失败退出）。
func streamDeliverFrame(opts streamPtyOpts, opcode byte, payload []byte) (srvErr string, werr error) {
	if opcode == wsOpBinary { // 终端原始字节：直接落屏
		_, err := os.Stdout.Write(payload)
		return "", err
	}
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err == nil && asString(msg["type"]) == "error" {
		// 服务端错误帧：--json 下也照样识别，保证退出码非 0。
		text := asString(msg["data"])
		if text == "" {
			text = asString(msg["message"])
		}
		if text == "" {
			text = string(payload)
		}
		srvErr = text
	}
	if opts.JSONDump { // --json：原样打印文本帧
		_, err := os.Stdout.Write(append(payload, '\n'))
		return srvErr, err
	}
	if msg == nil {
		_, werr := os.Stdout.Write(payload) // 非 JSON 文本帧：原样落屏
		return srvErr, werr
	}
	typ := asString(msg["type"])
	data := asString(msg["data"])
	switch typ {
	case "ready":
		note := fmt.Sprintf("（已连接 WebSocket：session_id=%s", asString(msg["session_id"]))
		if b := asString(msg["backend"]); b != "" {
			note += " backend=" + b
		}
		if tr := asString(msg["transport"]); tr != "" {
			note += " transport=" + tr
		}
		if r, ok := msg["resumed"].(bool); ok && r {
			note += " resumed=true"
		}
		fmt.Fprintln(os.Stderr, note+"）")
	case "terminal_restored":
		fmt.Fprintln(os.Stderr, "（终端会话已恢复）")
	case "restore_output", "output", "data":
		if data != "" {
			_, err := os.Stdout.WriteString(data)
			return srvErr, err
		}
	case "error":
		text := srvErr
		if extra := asString(msg["hint"]); extra != "" {
			text += "（hint: " + extra + "）"
		}
		fmt.Fprintf(os.Stderr, "（服务端错误: %s）\n", text)
	default:
		if data != "" { // 未知类型但带 data：按输出处理
			_, err := os.Stdout.WriteString(data)
			return srvErr, err
		}
		_, err := os.Stdout.Write(append(payload, '\n')) // 完全未知：原样打印
		return srvErr, err
	}
	return srvErr, nil
}

// streamRunPty 是 pty-ws / pty-ws-anon 的交互主循环：
// stdin ↔ socket 双向转发；收到 close 帧 / stdin EOF / Ctrl-] / SIGINT 时退出。
// 退出码约定：用户主动退出（Ctrl-] / signal / stdin EOF）与对端正常关闭都返回 nil；
// 只有协议级错误或服务端 error 消息后才断开的情况返回非 nil（退出码非 0）。
func streamRunPty(w *wsConn, opts streamPtyOpts) error {
	var (
		quitOnce sync.Once
		quitMu   sync.Mutex
		quitWhy  string
	)
	setQuit := func(why string) {
		quitOnce.Do(func() {
			quitMu.Lock()
			quitWhy = why
			quitMu.Unlock()
			w.shutdown(1000, why) // 发 close 帧并关底层连接，同时唤醒读循环
		})
	}
	getQuit := func() string {
		quitMu.Lock()
		defer quitMu.Unlock()
		return quitWhy
	}

	// SIGINT / SIGTERM：优雅断开（测试脚本与用户 Ctrl-C 都走这里）。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			fmt.Fprintln(os.Stderr, "\n（收到中断信号，断开连接）")
			setQuit("signal")
		}
	}()

	// stdin → socket。json 模式下把不完整的 UTF-8 尾巴留到下一块，避免半个字符被 JSON 转义吃掉。
	go func() {
		chunk := make([]byte, 4096)
		var pending []byte
		for {
			n, err := os.Stdin.Read(chunk)
			if n > 0 {
				merged := make([]byte, 0, len(pending)+n)
				merged = append(merged, pending...)
				merged = append(merged, chunk[:n]...)
				pending = nil
				if i := bytes.IndexByte(merged, 0x1d); i >= 0 { // Ctrl-] 退出
					if i > 0 {
						_ = streamSendInput(w, opts, merged[:i])
					}
					fmt.Fprintln(os.Stderr, "（收到 Ctrl-]，退出）")
					setQuit("ctrl-]")
					return
				}
				if opts.Protocol == "json" {
					head, tail := wsUTF8Split(merged)
					pending = tail
					if len(head) > 0 && streamSendInput(w, opts, head) != nil {
						setQuit("发送失败")
						return
					}
				} else if streamSendInput(w, opts, merged) != nil {
					setQuit("发送失败")
					return
				}
			}
			if err != nil { // EOF = 输入结束，主动断开
				setQuit("stdin EOF")
				return
			}
		}
	}()

	// 主循环：socket → stdout（分片消息在本地重组）。
	var (
		msgOp         byte
		msgBuf        []byte
		lastServerErr string // 服务端 error 消息（决定最终退出码）
	)
	for {
		fin, opcode, payload, err := w.readFrame()
		if err != nil {
			if why := getQuit(); why != "" {
				return nil // 主动退出：提示已在触发点打印
			}
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) {
				if lastServerErr != "" {
					return fmt.Errorf("服务端报错后断开了连接: %s", lastServerErr)
				}
				fmt.Fprintln(os.Stderr, "（服务端关闭了连接）")
				return nil
			}
			return fmt.Errorf("读取 WebSocket 帧失败: %w", err)
		}
		switch opcode {
		case wsOpPing: // 回 pong（payload 原样带回）
			if err := w.writeFrame(wsOpPong, true, payload); err != nil {
				return fmt.Errorf("回复 pong 失败: %w", err)
			}
		case wsOpPong:
			// 忽略心跳应答
		case wsOpClose:
			code, reason := wsParseClose(payload)
			if reason != "" {
				fmt.Fprintf(os.Stderr, "（服务端请求关闭连接：code=%d reason=%q）\n", code, reason)
			} else {
				fmt.Fprintf(os.Stderr, "（服务端请求关闭连接：code=%d）\n", code)
			}
			w.shutdown(code, "") // 回一个 close 帧
			if lastServerErr != "" {
				return fmt.Errorf("服务端报错后关闭了连接: %s", lastServerErr)
			}
			return nil
		case wsOpText, wsOpBinary:
			if !fin { // 分片消息起点：暂存，等 continuation
				msgOp = opcode
				msgBuf = append(msgBuf[:0], payload...)
				if len(msgBuf) > wsMaxFrameSize {
					return fmt.Errorf("分片消息过大（> %d 字节）", wsMaxFrameSize)
				}
				continue
			}
			if srvErr, err := streamDeliverFrame(opts, opcode, payload); err != nil {
				return fmt.Errorf("写出输出失败: %w", err)
			} else if srvErr != "" {
				lastServerErr = srvErr
			}
		case wsOpContinuation:
			msgBuf = append(msgBuf, payload...)
			if len(msgBuf) > wsMaxFrameSize {
				return fmt.Errorf("分片消息过大（> %d 字节）", wsMaxFrameSize)
			}
			if fin {
				buf, op := msgBuf, msgOp
				msgBuf, msgOp = nil, 0
				if srvErr, err := streamDeliverFrame(opts, op, buf); err != nil {
					return fmt.Errorf("写出输出失败: %w", err)
				} else if srvErr != "" {
					lastServerErr = srvErr
				}
			}
		default:
			return fmt.Errorf("收到未知 opcode %#x", opcode)
		}
	}
}

// ---------------------------------------------------------------- pty-ws / pty-ws-anon

// cmdPtyWS：WebSocket 附着到已有 pty 会话 GET /v2/pty/sessions/{id}/ws。
func cmdPtyWS(args []string) error {
	usage := "pty-ws <会话id> [--protocol=json|binary] [--durable] [--restore] [--replay-bytes=N]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	protocol, err := streamProtocol(flags)
	if err != nil {
		return err
	}
	query := map[string]string{"protocol": protocol}
	if flagBool(flags, "durable") {
		query["durable"] = "true"
	}
	if flagBool(flags, "restore") {
		query["restore"] = "true"
	}
	if v := strings.TrimSpace(flags["replay-bytes"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("--replay-bytes 不是合法非负整数: %q", v)
		}
		query["replay_bytes"] = strconv.Itoa(n)
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	w, err := wsDial(c, "/v2/pty/sessions/"+url.PathEscape(id)+"/ws", query)
	if err != nil {
		return fmt.Errorf("连接 pty 会话 %s 的 WebSocket 失败: %w", id, err)
	}
	// 被 defer 的 close 是兜底：正常路径下 streamRunPty 里已关闭（幂等）。
	defer w.close()
	fmt.Fprintf(os.Stderr, "已附着 pty 会话 %s（protocol=%s；Ctrl-] 退出）\n", id, protocol)
	return streamRunPty(w, streamPtyOpts{Protocol: protocol, JSONDump: flagBool(flags, "json")})
}

// cmdPtyWSAnon：匿名 WebShell GET /v2/pty/ws（连接级会话：连接即用、断开即销毁）。
func cmdPtyWSAnon(args []string) error {
	_, flags := splitFlags(args)
	protocol, err := streamProtocol(flags)
	if err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	w, err := wsDial(c, "/v2/pty/ws", map[string]string{"protocol": protocol})
	if err != nil {
		return fmt.Errorf("连接匿名 WebShell WebSocket 失败: %w", err)
	}
	defer w.close()
	fmt.Fprintf(os.Stderr, "已连接匿名 WebShell（protocol=%s；断开即销毁；Ctrl-] 退出）\n", protocol)
	return streamRunPty(w, streamPtyOpts{Protocol: protocol, JSONDump: flagBool(flags, "json")})
}

// ---------------------------------------------------------------- watch-events（SSE）

// cmdWatchEvents：GET /v2/watch/{watcher_id}/events，逐事件解析并打印 SSE 流。
//
// --max=N：收到（并打印）N 个事件后正常退出；Ctrl-C 优雅退出（退出码 0）。
// 人类可读模式每个事件打两段：
//
//	[#序号] event=<事件名> id=<id>
//	  <data（JSON 时美化；否则原样）>
//
// --json：只打印原始 data 行（多行 data 原样拼接）。
func cmdWatchEvents(args []string) error {
	usage := "watch-events <watcher_id> [--max=N]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	maxEvents := 0
	if v := strings.TrimSpace(flags["max"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("--max 不是合法正整数: %q", v)
		}
		maxEvents = n
	}
	jsonOut := flagBool(flags, "json")
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	interrupted := make(chan struct{})
	go func() {
		if _, ok := <-sigCh; ok {
			fmt.Fprintln(os.Stderr, "\n（收到 Ctrl-C，退出事件流）")
			close(interrupted)
			cancel() // 中断阻塞中的流读取
		}
	}()

	req, err := c.newRequest(ctx, "GET", "/v2/watch/"+url.PathEscape(id)+"/events", nil, nil, true)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		select {
		case <-interrupted:
			return nil
		default:
		}
		return fmt.Errorf("订阅 watcher %s 事件流失败: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("订阅 watcher %s 事件流失败: HTTP %d: %s", id, resp.StatusCode, firstLine(body))
	}
	ctype := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ctype), "text/event-stream") {
		if ctype == "" {
			ctype = "(空)"
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("服务端未提供 SSE 事件流（Content-Type=%s）: %s", ctype, firstLine(body))
	}
	if !jsonOut {
		fmt.Fprintf(os.Stderr, "已订阅 watcher %s 的事件流（Ctrl-C 退出）\n", id)
	}

	br := bufio.NewReaderSize(resp.Body, 256*1024)
	var (
		evName string
		evID   string
		evData []string
		count  int
	)
	// dispatch 打印一个完整事件；返回 (是否达到 --max, error)。
	dispatch := func() (bool, error) {
		if len(evData) == 0 {
			evName, evID, evData = "", "", nil
			return false, nil
		}
		count++
		if jsonOut {
			// --json：原始 data 行（多行 data 按 SSE 规范用 \n 拼接后原样输出）
			fmt.Fprintln(os.Stdout, strings.Join(evData, "\n"))
		} else {
			head := fmt.Sprintf("[#%d] event=%s", count, streamOr(evName, "message"))
			if evID != "" {
				head += " id=" + evID
			}
			fmt.Fprintln(os.Stdout, head)
			streamPrintEventData(evData)
		}
		evName, evID, evData = "", "", nil
		return maxEvents > 0 && count >= maxEvents, nil
	}
	for {
		line, err := streamReadLine(br, 4<<20)
		if err != nil {
			select {
			case <-interrupted:
				return nil
			default:
			}
			if errors.Is(err, io.EOF) {
				if done, _ := dispatch(); done {
					fmt.Fprintf(os.Stderr, "（已达 --max=%d，正常退出）\n", maxEvents)
					return nil
				}
				fmt.Fprintln(os.Stderr, "（事件流已结束）")
				return nil
			}
			return fmt.Errorf("读取 SSE 流失败: %w", err)
		}
		line = strings.TrimRight(line, "\r")
		if line == "" { // 空行 = 分发事件
			done, derr := dispatch()
			if derr != nil {
				return derr
			}
			if done {
				fmt.Fprintf(os.Stderr, "（已达 --max=%d，正常退出）\n", maxEvents)
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // 注释行（心跳/keep-alive），忽略
		}
		name, val := line, ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			name, val = line[:i], line[i+1:]
			if strings.HasPrefix(val, " ") {
				val = val[1:] // SSE 规范：字段值去掉一个前导空格
			}
		}
		switch name {
		case "event":
			evName = val
		case "data":
			evData = append(evData, val)
		case "id":
			evID = val
		case "retry":
			// 重连间隔：本命令不做自动重连，忽略
		}
	}
}

// streamReadLine 从 SSE 流读取一行（不含行尾换行；允许最后一行缺 \n）。
// 单行超过 max 字节直接报错（防异常服务端打爆内存）。
func streamReadLine(br *bufio.Reader, max int) (string, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return "", fmt.Errorf("SSE 单行过长（> %d 字节）", max)
		}
		if err == bufio.ErrBufferFull {
			continue // 行比缓冲区长：继续攒
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(buf) > 0 {
					return string(buf), nil // 末行无换行符
				}
				return "", io.EOF
			}
			return "", err
		}
		return string(buf[:len(buf)-1]), nil // 去掉 \n
	}
}

// streamPrintEventData 人类可读地打印事件 data：JSON 美化缩进，其余原样（各带 2 空格缩进）。
func streamPrintEventData(lines []string) {
	joined := strings.Join(lines, "\n")
	var pretty any
	if json.Unmarshal([]byte(joined), &pretty) == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			for _, ln := range strings.Split(string(b), "\n") {
				fmt.Fprintln(os.Stdout, "  "+ln)
			}
			return
		}
	}
	for _, ln := range lines {
		fmt.Fprintln(os.Stdout, "  "+ln)
	}
}

// streamOr 返回 s，空串时返回 def。
func streamOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
