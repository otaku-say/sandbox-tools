// v2_fs.go —— 文件面（/v2/fs/*）命令实现（纯 v2 HTTP）
//
// 覆盖命令：read / cat / write / ls / stat / tree / edit / grep / search /
// mkdir / cp / mv / rm / put / get。每个命令都支持 --json。
//
// 实测备注（细节见交付报告）：
//   - /v2/fs/tree 返回 tar 原始字节（Content-Type: application/x-tar），不是 JSON；
//     --depth 由本地渲染限制层数，服务端不消费 depth/max_depth 参数。
//   - /v2/fs/edit 的 replace_mode 合法值为 ALL | FIRST | LAST（大写）。
//   - /v2/fs/list 的 show_hidden 服务端缺省为 true；本 CLI 默认显式传 false，
//     用 --hidden 打开，与传统 ls 语义一致。
//   - /v2/fs/write 会自动创建缺失的父目录；/v2/fs/upload 同名文件直接覆盖。
package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------- 私有工具（fs 前缀）

// fsJSON 是否设置了 --json。
func fsJSON(flags map[string]string) bool { return flagBool(flags, "json") }

// fsUser 取通用 --user= 值（为空表示不传）。
func fsUser(flags map[string]string) string { return flags["user"] }

// fsQueryUser 只有 --user 非空时才生成 query map。
func fsQueryUser(flags map[string]string) map[string]string {
	if u := fsUser(flags); u != "" {
		return map[string]string{"user": u}
	}
	return nil
}

// fsPutIf 非空值才写入 query。
func fsPutIf(q map[string]string, k, v string) {
	if v != "" {
		q[k] = v
	}
}

// fsBool 宽松取布尔字段（缺省 false）。
func fsBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// fsFmtTime 把 unix 秒（字符串或数字）格式化成本地时间；空/0 显示 "-"。
func fsFmtTime(v any) string {
	s := asString(v)
	if s == "" || s == "0" {
		return "-"
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s // 不是 unix 秒就原样显示
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

// fsSplitCSV 解析逗号分隔列表（用于 --include=*.go,*.py）。
func fsSplitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------- read / cat

// cmdRead：GET /v2/fs/read，默认打印 content；--json 打印完整 data。
func cmdRead(args []string) error {
	return fsReadCmd(args, "read <path> [--start=] [--end=] [--user=]")
}

// cmdCat：与 read 相同（cat 面向“直接看内容”）。
func cmdCat(args []string) error {
	return fsReadCmd(args, "cat <path> [--start=] [--end=]")
}

func fsReadCmd(args []string, usage string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	q := map[string]string{"path": pos[0]}
	// 行号 0 起；end_line 不含尾行（服务端语义，原样透传）。
	if v, ok := flags["start"]; ok && v != "" {
		q["start_line"] = v
	}
	if v, ok := flags["end"]; ok && v != "" {
		q["end_line"] = v
	}
	fsPutIf(q, "user", fsUser(flags))
	data, err := c.api("GET", "/v2/fs/read", q, nil)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", pos[0], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	content := asString(asMap(data)["content"])
	fmt.Print(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		fmt.Println() // 行区间读取时服务端不带尾换行，这里补一个保证终端输出完整
	}
	return nil
}

// ---------------------------------------------------------------- write

// cmdWrite：POST /v2/fs/write；本地文件为 `-` 时从 stdin 读取。
func cmdWrite(args []string) error {
	usage := "write <本地文件|-> <远端路径> [--append] [--user=]"
	// 注意：单独的 `-` 会被 splitFlags 当作布尔开关吃掉，先对原始参数扫描。
	stdinMode := false
	for _, a := range args {
		if a == "-" {
			stdinMode = true
		}
	}
	pos, flags := splitFlags(args)
	var local, remote string
	if stdinMode {
		if len(pos) != 1 {
			return fmt.Errorf("stdin 模式用法: %s", usage)
		}
		remote = pos[0]
	} else {
		if err := needArgs(pos, 2, usage); err != nil {
			return err
		}
		local, remote = pos[0], pos[1]
	}
	src := "stdin"
	var content []byte
	var err error
	if stdinMode {
		if content, err = io.ReadAll(os.Stdin); err != nil {
			return fmt.Errorf("读取 stdin 失败: %w", err)
		}
	} else {
		src = local
		if content, err = os.ReadFile(local); err != nil {
			return fmt.Errorf("读取本地文件 %s 失败: %w", local, err)
		}
	}
	if !utf8.Valid(content) {
		fmt.Fprintln(os.Stderr, "警告: 内容含非法 UTF-8 字节，JSON 传输可能损坏二进制；大文件/二进制请用 put 命令")
	}
	body := map[string]any{"path": remote, "content": string(content)}
	if flagBool(flags, "append") {
		body["append"] = true
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/fs/write", fsQueryUser(flags), body)
	if err != nil {
		return fmt.Errorf("写入 %s 失败: %w", remote, err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	f := asString(m["file"])
	if f == "" {
		f = remote
	}
	verb := "已写入"
	if flagBool(flags, "append") {
		verb = "已追加写入"
	}
	fmt.Printf("%s %s（%d 字节，来源 %s）\n", verb, f, asInt(m["bytes_written"], len(content)), src)
	return nil
}

// ---------------------------------------------------------------- ls

// cmdLs：GET /v2/fs/list，人类可读清单（默认隐藏 . 开头文件，--hidden 才显示）。
func cmdLs(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "ls <path> [--recursive] [--hidden] [--depth=] [--user=]"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	q := map[string]string{"path": pos[0]}
	if flagBool(flags, "recursive") {
		q["recursive"] = "true"
	}
	// 服务端实测缺省 show_hidden=true（默认就含隐藏文件）；这里默认显式传 false、
	// --hidden 才传 true，保证 --hidden 开关有实际语义。
	if flagBool(flags, "hidden") {
		q["show_hidden"] = "true"
	} else {
		q["show_hidden"] = "false"
	}
	if v, ok := flags["depth"]; ok && v != "" {
		q["max_depth"] = v
	}
	fsPutIf(q, "user", fsUser(flags))
	data, err := c.api("GET", "/v2/fs/list", q, nil)
	if err != nil {
		return fmt.Errorf("列出 %s 失败: %w", pos[0], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	fsPrintFileList(data)
	return nil
}

// fsPrintFileList 渲染 list 结果：类型/大小/修改时间/路径 + 汇总行。
func fsPrintFileList(data any) {
	m := asMap(data)
	files := append([]any(nil), asList(m["files"])...)
	if len(files) == 0 {
		fmt.Println("（空）")
	} else {
		// 按完整路径排序，让递归列表输出稳定可读。
		sort.SliceStable(files, func(i, j int) bool {
			return asString(asMap(files[i])["path"]) < asString(asMap(files[j])["path"])
		})
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, f := range files {
			fm := asMap(f)
			name := asString(fm["path"])
			if name == "" {
				name = asString(fm["name"])
			}
			typ := "-"
			switch {
			case fsBool(fm["is_directory"]):
				typ = "d"
			case fsBool(fm["is_symlink"]):
				typ = "l"
			}
			size := "-"
			if !fsBool(fm["is_directory"]) {
				if s := asString(fm["size"]); s != "" {
					size = s
				} else {
					size = "0"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", typ, size, fsFmtTime(fm["modified_time"]), name)
		}
		w.Flush()
	}
	if _, ok := m["total_count"]; ok {
		fmt.Printf("共 %s 项：目录 %s，文件 %s", asString(m["total_count"]), asString(m["directory_count"]), asString(m["file_count"]))
		if fsBool(m["truncated"]) {
			fmt.Print("（已截断）")
		}
		fmt.Println()
	}
}

// ---------------------------------------------------------------- stat

// cmdStat：GET /v2/fs/stat；路径不存在时服务端返回 404 → 返回错误（退出码非 0）。
func cmdStat(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "stat <path> [--user=]"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	q := map[string]string{"path": pos[0]}
	fsPutIf(q, "user", fsUser(flags))
	data, err := c.api("GET", "/v2/fs/stat", q, nil)
	if err != nil {
		return fmt.Errorf("查看 %s 失败: %w", pos[0], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	kind := "文件"
	if fsBool(m["is_directory"]) {
		kind = "目录"
	}
	if fsBool(m["is_symlink"]) {
		kind = "符号链接"
	}
	fmt.Printf("路径:     %s\n", asString(m["path"]))
	fmt.Printf("类型:     %s\n", kind)
	fmt.Printf("大小:     %s 字节\n", asString(m["size"]))
	fmt.Printf("权限:     %s\n", asString(m["permissions"]))
	fmt.Printf("修改时间: %s\n", fsFmtTime(m["modified_time"]))
	return nil
}

// ---------------------------------------------------------------- tree

// fsTarEntry 是从 /v2/fs/tree 返回的 tar 中解析出的一条记录。
type fsTarEntry struct {
	Name  string // 相对路径，如 "sub/deep/x.txt"
	IsDir bool
	Size  int64
	Mode  string // 权限位，如 "644"
	MTime int64  // unix 秒
}

// cmdTree：GET /v2/fs/tree。该接口返回 tar 原始字节（实测），这里解析后渲染目录树；
// --depth=N 限制本地显示层数（服务端不消费 depth/max_depth 参数，实测均被忽略）。
func cmdTree(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "tree <path> [--depth=]"); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("GET", "/v2/fs/tree", map[string]string{"path": pos[0]}, nil, 0)
	if err != nil {
		return fmt.Errorf("获取 %s 目录树失败: %w", pos[0], err)
	}
	entries, err := fsParseTar(raw)
	if err != nil {
		return fmt.Errorf("解析 %s 的 tar 数据失败: %w", pos[0], err)
	}
	maxDepth := -1 // 默认不限层数
	if v, ok := flags["depth"]; ok && v != "" {
		maxDepth = parseInt(v, -1)
	}
	if fsJSON(flags) {
		// tree 的服务端 data 是 tar 二进制，没有 JSON 可原样打印；输出解析后的条目数组。
		printJSON(map[string]any{"path": pos[0], "entries": fsTarEntriesJSON(entries)})
		return nil
	}
	fsPrintTree(pos[0], entries, maxDepth)
	return nil
}

// fsParseTar 遍历 tar 流，收集目录/文件条目（忽略 `./` 根条目）。
func fsParseTar(raw []byte) ([]fsTarEntry, error) {
	var out []fsTarEntry
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "./"), "/")
		if name == "" || name == "." {
			continue
		}
		out = append(out, fsTarEntry{
			Name:  name,
			IsDir: hdr.Typeflag == tar.TypeDir,
			Size:  hdr.Size,
			Mode:  fmt.Sprintf("%04o", hdr.Mode&0o777),
			MTime: hdr.ModTime.Unix(),
		})
	}
	return out, nil
}

// fsTarEntriesJSON 转成 --json 输出用的结构（字段风格对齐 /v2/fs/stat）。
func fsTarEntriesJSON(entries []fsTarEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"path":          e.Name,
			"is_directory":  e.IsDir,
			"size":          e.Size,
			"permissions":   e.Mode,
			"modified_time": strconv.FormatInt(e.MTime, 10),
		})
	}
	return out
}

// fsNode 是 tree 渲染用的树节点。
type fsNode struct {
	name     string
	isDir    bool
	children map[string]*fsNode
}

// fsBuildNodeTree 把扁平条目组装成树；中间缺失的目录条目会自动补建。
func fsBuildNodeTree(entries []fsTarEntry) *fsNode {
	root := &fsNode{name: ".", isDir: true, children: map[string]*fsNode{}}
	for _, e := range entries {
		parts := strings.Split(e.Name, "/")
		cur := root
		for i, part := range parts {
			child, ok := cur.children[part]
			if !ok {
				child = &fsNode{name: part, children: map[string]*fsNode{}}
				cur.children[part] = child
			}
			if i == len(parts)-1 {
				child.isDir = e.IsDir
			} else {
				child.isDir = true // 有下级路径段，说明它一定是目录
			}
			cur = child
		}
	}
	return root
}

// fsPrintTree 以 tree(1) 风格渲染目录树；maxDepth<0 表示不限层数。
func fsPrintTree(rootPath string, entries []fsTarEntry, maxDepth int) {
	root := fsBuildNodeTree(entries)
	dirs, files := 0, 0
	fmt.Println(rootPath)
	var walk func(n *fsNode, prefix string, level int)
	walk = func(n *fsNode, prefix string, level int) {
		names := make([]string, 0, len(n.children))
		for k := range n.children {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool {
			a, b := n.children[names[i]], n.children[names[j]]
			if a.isDir != b.isDir {
				return a.isDir // 目录排在文件前
			}
			return names[i] < names[j]
		})
		for i, name := range names {
			if maxDepth >= 0 && level+1 > maxDepth {
				break // 同层节点深度一致，超限即整层截断
			}
			ch := n.children[name]
			branch, nextPrefix := "├── ", prefix+"│   "
			if i == len(names)-1 {
				branch, nextPrefix = "└── ", prefix+"    "
			}
			if ch.isDir {
				dirs++
				fmt.Printf("%s%s%s/\n", prefix, branch, ch.name)
				if maxDepth < 0 || level+2 <= maxDepth {
					walk(ch, nextPrefix, level+1)
				}
			} else {
				files++
				fmt.Printf("%s%s%s\n", prefix, branch, ch.name)
			}
		}
	}
	walk(root, "", 0)
	fmt.Printf("\n%d 个目录, %d 个文件\n", dirs, files)
}

// ---------------------------------------------------------------- edit

// cmdEdit：POST /v2/fs/edit，两种模式：
//   - str_replace：--old= --new= [--replace-all]（replace_mode=ALL，多命中时必需）；
//   - insert：--insert=行号 --text=内容。
//
// --replace-all 时若服务端不认 replace_mode（老版本），退化为“仅替换首处”并明确提示。
func cmdEdit(args []string) error {
	usage := "edit <path> --old=<旧串> --new=<新串> [--replace-all] | edit <path> --insert=<行号> --text=<内容>"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	p := pos[0]
	_, hasOld := flags["old"]
	_, hasInsert := flags["insert"]

	var data any
	switch {
	case hasOld:
		newStr, hasNew := flags["new"]
		if !hasNew {
			return fmt.Errorf("--old 模式需要 --new（允许空串表示删除）。用法: %s", usage)
		}
		body := map[string]any{"path": p, "command": "str_replace", "old_str": flags["old"], "new_str": newStr}
		if flagBool(flags, "replace-all") {
			body["replace_mode"] = "ALL" // 服务端合法值：ALL | FIRST | LAST（大写）
		}
		data, err = c.api("POST", "/v2/fs/edit", fsQueryUser(flags), body)
		if err != nil && flagBool(flags, "replace-all") && strings.Contains(strings.ToLower(err.Error()), "replace_mode") {
			fmt.Fprintln(os.Stderr, "提示: 服务端不支持 replace_mode=ALL，已退化为仅替换首处")
			data, err = fsReplaceFirst(c, p, flags["old"], newStr, fsQueryUser(flags))
		}
		if err != nil {
			return fmt.Errorf("编辑 %s 失败: %w", p, err)
		}
	case hasInsert:
		text, hasText := flags["text"]
		if !hasText {
			return fmt.Errorf("--insert 模式需要 --text=内容。用法: %s", usage)
		}
		body := map[string]any{"path": p, "command": "insert", "insert_line": parseInt(flags["insert"], 0), "new_str": text}
		data, err = c.api("POST", "/v2/fs/edit", fsQueryUser(flags), body)
		if err != nil {
			return fmt.Errorf("编辑 %s 失败: %w", p, err)
		}
	default:
		return fmt.Errorf("需要 --old=/--new= 或 --insert=/--text=。用法: %s", usage)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	// 打印服务端返回的替换结果（data.output 含修改后的 cat -n 片段）。
	if out := asString(asMap(data)["output"]); out != "" {
		fmt.Println(out)
	} else {
		printData(data)
	}
	return nil
}

// fsReplaceFirst 是 replace_mode 不被支持时的兜底：读全文、只替换首个 old 再写回。
func fsReplaceFirst(c *Client, p, old, new string, query map[string]string) (any, error) {
	data, err := c.api("GET", "/v2/fs/read", map[string]string{"path": p}, nil)
	if err != nil {
		return nil, err
	}
	content := asString(asMap(data)["content"])
	i := strings.Index(content, old)
	if i < 0 {
		return nil, fmt.Errorf("old_str not found in file")
	}
	updated := content[:i] + new + content[i+len(old):]
	return c.api("POST", "/v2/fs/write", query, map[string]any{"path": p, "content": updated})
}

// ---------------------------------------------------------------- grep

// cmdGrep：POST /v2/fs/grep，默认递归（--recursive=false 可关）。
func cmdGrep(args []string) error {
	usage := "grep <path> <正则> [--fixed] [--ignore-case] [--include=*.go,*.py] [--exclude=] [--max=]"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, usage); err != nil {
		return err
	}
	body := map[string]any{"path": pos[0], "pattern": pos[1], "recursive": true}
	if _, ok := flags["recursive"]; ok {
		body["recursive"] = flagBool(flags, "recursive")
	}
	if flagBool(flags, "fixed") {
		body["fixed_strings"] = true
	}
	if flagBool(flags, "ignore-case") {
		body["case_insensitive"] = true
	}
	if v, ok := flags["include"]; ok && v != "" {
		body["include"] = fsSplitCSV(v)
	}
	if v, ok := flags["exclude"]; ok && v != "" {
		body["exclude"] = fsSplitCSV(v)
	}
	if v, ok := flags["max"]; ok && v != "" {
		body["max_results"] = parseInt(v, 0)
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/fs/grep", fsQueryUser(flags), body)
	if err != nil {
		return fmt.Errorf("grep %q 失败: %w", pos[1], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	matches := asList(m["matches"])
	for _, e := range matches {
		em := asMap(e)
		fmt.Printf("%s:%d: %s\n", asString(em["file"]), asInt(em["line_number"], 0), asString(em["line_content"]))
	}
	fmt.Printf("共 %d 处匹配（%d 个文件命中 / 扫描 %d 个文件）\n",
		asInt(m["match_count"], len(matches)), asInt(m["files_matched"], 0), asInt(m["files_searched"], 0))
	if fsBool(m["truncated"]) {
		fmt.Println("（结果已截断，可缩小范围或调大 --max=）")
	}
	return nil
}

// ---------------------------------------------------------------- search

// cmdSearch：GET /v2/fs/search（pattern 为 glob，如 **/*.py）。
func cmdSearch(args []string) error {
	usage := "search <path> <glob 如 **/*.py>"
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, usage); err != nil {
		return err
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	q := map[string]string{"path": pos[0], "pattern": pos[1]}
	fsPutIf(q, "user", fsUser(flags))
	data, err := c.api("GET", "/v2/fs/search", q, nil)
	if err != nil {
		return fmt.Errorf("搜索 %s 失败: %w", pos[1], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	files := asList(m["files"])
	for _, f := range files {
		fmt.Println(asString(f))
	}
	fmt.Printf("共 %d 个文件", len(files))
	if fsBool(m["truncated"]) {
		fmt.Print("（已截断）")
	}
	fmt.Println()
	return nil
}

// ---------------------------------------------------------------- mkdir / cp / mv / rm

// cmdMkdir：POST /v2/fs/mkdir。
func cmdMkdir(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "mkdir <path> [--parents]"); err != nil {
		return err
	}
	body := map[string]any{"path": pos[0]}
	if flagBool(flags, "parents") {
		body["parents"] = true
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/fs/mkdir", fsQueryUser(flags), body)
	if err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", pos[0], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	p := asString(m["path"])
	if p == "" {
		p = pos[0]
	}
	if _, ok := m["created"]; ok && !fsBool(m["created"]) {
		fmt.Printf("目录已存在: %s\n", p)
	} else {
		fmt.Printf("已创建目录: %s\n", p)
	}
	return nil
}

// cmdCp：POST /v2/fs/copy。
func cmdCp(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, "cp <源> <目标> [--overwrite]"); err != nil {
		return err
	}
	return fsCopyMove("copy", "已复制", pos[0], pos[1], flagBool(flags, "overwrite"), flags)
}

// cmdMv：POST /v2/fs/move。
func cmdMv(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, "mv <源> <目标> [--overwrite]"); err != nil {
		return err
	}
	return fsCopyMove("move", "已移动", pos[0], pos[1], flagBool(flags, "overwrite"), flags)
}

// fsCopyMove 复用的 copy/move 实现。
func fsCopyMove(route, verb, src, dst string, overwrite bool, flags map[string]string) error {
	body := map[string]any{"source": src, "destination": dst}
	if overwrite {
		body["overwrite"] = true
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/fs/"+route, fsQueryUser(flags), body)
	if err != nil {
		// 出错时用中性动词（“复制/移动”），避免出现“已移动…失败”的别扭措辞。
		desc := "移动"
		if route == "copy" {
			desc = "复制"
		}
		return fmt.Errorf("%s %s -> %s 失败: %w", desc, src, dst, err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	if route == "copy" {
		m := asMap(data)
		fmt.Printf("%s %s -> %s（%d 个文件，%d 字节）\n", verb, src, dst,
			asInt(m["files_copied"], -1), asInt(m["bytes_copied"], -1))
	} else {
		fmt.Printf("%s %s -> %s\n", verb, src, dst)
	}
	return nil
}

// cmdRm：POST /v2/fs/delete（目录非空时必须 --recursive）。
func cmdRm(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 1, "rm <路径> [--recursive]"); err != nil {
		return err
	}
	body := map[string]any{"path": pos[0]}
	if flagBool(flags, "recursive") {
		body["recursive"] = true
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	data, err := c.api("POST", "/v2/fs/delete", fsQueryUser(flags), body)
	if err != nil {
		return fmt.Errorf("删除 %s 失败: %w", pos[0], err)
	}
	if fsJSON(flags) {
		printJSON(data)
		return nil
	}
	m := asMap(data)
	p := asString(m["path"])
	if p == "" {
		p = pos[0]
	}
	kind := "文件"
	if fsBool(m["is_directory"]) {
		kind = "目录"
	}
	fmt.Printf("已删除%s: %s\n", kind, p)
	return nil
}

// ---------------------------------------------------------------- put / get

// cmdPut：先 POST /v2/fs/upload（multipart 字段 file，落盘 /tmp/<文件名>），
// 再 POST /v2/fs/move 移动到目标路径；--overwrite 时 move 带 overwrite。
func cmdPut(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, "put <本地文件> <远端路径> [--overwrite]"); err != nil {
		return err
	}
	local, remote := pos[0], pos[1]
	content, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("读取本地文件 %s 失败: %w", local, err)
	}
	c, err := mustClient()
	if err != nil {
		return err
	}
	up, err := c.apiMultipart("/v2/fs/upload", "file", path.Base(local), content)
	if err != nil {
		return fmt.Errorf("上传 %s 失败: %w", local, err)
	}
	tmp := asString(asMap(up)["file_path"])
	if tmp == "" {
		return fmt.Errorf("上传响应缺少 file_path: %v", asMap(up))
	}
	body := map[string]any{"source": tmp, "destination": remote}
	if flagBool(flags, "overwrite") {
		body["overwrite"] = true
	}
	mv, err := c.api("POST", "/v2/fs/move", fsQueryUser(flags), body)
	if err != nil {
		return fmt.Errorf("移动上传文件 %s -> %s 失败: %w", tmp, remote, err)
	}
	// 边角保护：当 CLI 与目标机共享文件系统、且本地文件恰好位于上传落盘路径上时
	// （例如在沙箱内直接跑本 CLI：本地 /tmp/x 与远端落盘 /tmp/x 同机同名），
	// 上面的 move 会把“本地文件本身”搬走。这里把内容补写回本地，保证 put 不吞本地文件。
	if localAbs, aerr := filepath.Abs(local); aerr == nil && localAbs == tmp {
		if werr := os.WriteFile(local, content, 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "警告: 补写本地文件 %s 失败: %v\n", local, werr)
		}
	}
	if fsJSON(flags) {
		// put 由两次服务端调用组成，这里合并给出两段原始 data。
		printJSON(map[string]any{"upload": up, "move": mv})
		return nil
	}
	fmt.Printf("已上传 %s -> %s（%d 字节）\n", local, remote, len(content))
	return nil
}

// cmdGet：GET /v2/fs/download 返回原始字节（apiRaw，二进制安全），写入本地文件。
func cmdGet(args []string) error {
	pos, flags := splitFlags(args)
	if err := needArgs(pos, 2, "get <远端路径> <本地文件>"); err != nil {
		return err
	}
	remote, local := pos[0], pos[1]
	c, err := mustClient()
	if err != nil {
		return err
	}
	raw, err := c.apiRaw("GET", "/v2/fs/download", map[string]string{"path": remote}, nil, 0)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", remote, err)
	}
	if err := os.WriteFile(local, raw, 0o644); err != nil {
		return fmt.Errorf("写入本地文件 %s 失败: %w", local, err)
	}
	if fsJSON(flags) {
		// download 返回原始字节，无 JSON data；--json 输出本地统计。
		printJSON(map[string]any{"path": remote, "local": local, "bytes": len(raw)})
		return nil
	}
	fmt.Printf("已下载 %s -> %s（%d 字节）\n", remote, local, len(raw))
	return nil
}
