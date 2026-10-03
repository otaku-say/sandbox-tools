// v2_watch.go —— 监听面（/v2/watch*）命令实现（纯 v2 HTTP）
//
// 覆盖命令：watch / watch-poll / watch-ls / watch-rm，全部支持 --json
// （置位时原样打印服务端 data）。
//
// 实测备注（aio-daemon v0.9.2；原始输出见交付报告）：
//   - POST /v2/watch 的 data：{watcher_id,path,status,created_at,reused,initial_cursor}；
//     同路径重复创建会复用已有 watcher（reused=true，返回同一 id）。
//   - poll 的 data：{events:[{seq,type,path,relative_path,is_dir,timestamp,...}],cursor,overflow}；
//     timeout=<秒> 时空轮询会阻塞至多 timeout 秒再返回（long-poll）；
//     不带 timeout 则立即返回当前增量。事件行按 "seq\ttype\tpath" 打印，
//     新 cursor 打一行到 stderr（cursor=N）方便脚本直接 --cursor=N 续读。
//   - DELETE /v2/watch/{id} 的 data：{watcher_id,status:"stopped"}；
//     偶见删除后列表短暂滞后（详见报告"实测与文档不符之处"）。
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// ---------------------------------------------------------------- 私有工具（wch 前缀防撞名）

// wchJSON 是否设置了 --json。
func wchJSON(flags map[string]string) bool { return flagBool(flags, "json") }

// wchSplitCSV 解析逗号分隔的模式列表（--include=*.go,*.txt）；空串返回 nil。
func wchSplitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------- watch

// cmdWatch：POST /v2/watch 创建目录监听；打印 watcher_id（附 path/recursive 说明）。
// 支持 --include=a,b（include_patterns）与 --exclude=a,b；两者也可写成
// --include-patterns= / --exclude-patterns=。
func cmdWatch(args []string) error {
	usage := "watch <路径> [--recursive] [--debounce=毫秒]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	body := map[string]any{"path": pos[0]}
	if flagBool(flags, "recursive") {
		body["recursive"] = true
	}
	inc := flags["include"]
	if inc == "" {
		inc = flags["include-patterns"]
	}
	if v := wchSplitCSV(inc); v != nil {
		body["include_patterns"] = v
	}
	exc := flags["exclude"]
	if exc == "" {
		exc = flags["exclude-patterns"]
	}
	if v := wchSplitCSV(exc); v != nil {
		body["exclude"] = v
	}
	if v := strings.TrimSpace(flags["debounce"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("--debounce 不是合法整数（毫秒）: %q", v)
		}
		body["debounce"] = n
	}
	data, err := c.api("POST", "/v2/watch", nil, body)
	if err != nil {
		return fmt.Errorf("创建监听 %s 失败: %w", pos[0], err)
	}
	if wchJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	line := "watcher_id=" + asString(m["watcher_id"])
	if p := asString(m["path"]); p != "" {
		line += "  path=" + p
	}
	// 实测创建响应不带 recursive 字段（只出现在请求体里）；响应缺失时回显请求意图。
	if r, ok := m["recursive"].(bool); ok {
		line += fmt.Sprintf("  recursive=%v", r)
	} else if flagBool(flags, "recursive") {
		line += "  recursive=true"
	}
	if re, ok := m["reused"].(bool); ok && re {
		line += "  reused=true"
	}
	fmt.Println(line)
	return nil
}

// ---------------------------------------------------------------- watch-poll

// cmdWatchPoll：GET /v2/watch/{id}/poll?cursor=&limit=&timeout=。
// 人类可读输出：每行 "seq\ttype\tpath"；新 cursor 输出一行到 stderr 方便续读。
func cmdWatchPoll(args []string) error {
	usage := "watch-poll <watcher_id> [--cursor=] [--timeout=] [--limit=]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	q := map[string]string{}
	for _, k := range []string{"cursor", "limit", "timeout"} {
		if v := strings.TrimSpace(flags[k]); v != "" {
			q[k] = v
		}
	}
	// HTTP 超时：服务端 timeout 为秒级 long-poll，多留 30s 缓冲。
	httpTO := 300 * time.Second
	if v := q["timeout"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			httpTO = time.Duration(f*float64(time.Second)) + 30*time.Second
		}
	}
	data, err := c.apiT("GET", "/v2/watch/"+url.PathEscape(id)+"/poll", q, nil, httpTO)
	if err != nil {
		return fmt.Errorf("轮询 watcher %s 失败: %w", id, err)
	}
	if wchJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	events := asList(m["events"])
	if len(events) == 0 {
		fmt.Println("（无新事件）")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, e := range events {
			em := asMap(e)
			p := asString(em["path"])
			if p == "" {
				p = asString(em["relative_path"])
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", asString(em["seq"]), asString(em["type"]), p)
		}
		w.Flush()
	}
	// 新 cursor 单独一行到 stderr：脚本可 `cursor=$(cli watch-poll ... 2>&1 >/dev/null)` 续读。
	if cur := asString(m["cursor"]); cur != "" {
		fmt.Fprintf(os.Stderr, "cursor=%s\n", cur)
	}
	if ov, ok := m["overflow"].(bool); ok && ov {
		fmt.Fprintln(os.Stderr, "overflow=true（事件过快，可能有丢弃）")
	}
	return nil
}

// ---------------------------------------------------------------- watch-ls

// cmdWatchLs：GET /v2/watch，表格打印 ID / STATUS / EVENTS / LATEST_SEQ / RECURSIVE / PATH。
func cmdWatchLs(args []string) error {
	_, flags := splitFlags(args)
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("GET", "/v2/watch", nil, nil)
	if err != nil {
		return fmt.Errorf("列出 watcher 失败: %w", err)
	}
	if wchJSON(flags) {
		printJSON(data)
		return nil
	}
	list := asList(data)
	if len(list) == 0 {
		fmt.Println("（无 watcher）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tEVENTS\tLATEST_SEQ\tRECURSIVE\tPATH")
	for _, it := range list {
		m := asMap(it)
		rec := "-"
		if b, ok := m["recursive"].(bool); ok {
			rec = fmt.Sprintf("%v", b)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			asString(m["watcher_id"]), asString(m["status"]),
			asString(m["event_count"]), asString(m["latest_seq"]), rec, asString(m["path"]))
	}
	w.Flush()
	fmt.Printf("共 %d 个 watcher\n", len(list))
	return nil
}

// ---------------------------------------------------------------- watch-rm

// cmdWatchRm：DELETE /v2/watch/{id}，停止并删除 watcher。
func cmdWatchRm(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "watch-rm <watcher_id>"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	id := pos[0]
	data, err := c.api("DELETE", "/v2/watch/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return fmt.Errorf("删除 watcher %s 失败: %w", id, err)
	}
	if wchJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	rid := asString(m["watcher_id"])
	if rid == "" {
		rid = id
	}
	if st := asString(m["status"]); st != "" {
		fmt.Printf("已停止 watcher: %s（status=%s）\n", rid, st)
	} else {
		fmt.Printf("已停止 watcher: %s\n", rid)
	}
	return nil
}
