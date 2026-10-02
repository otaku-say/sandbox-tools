package main

// cmd_file.go —— `file` 命名空间（client.File，17 个方法）
//
// 动作清单：
//
//	list  read  write  replace  search  find  grep  glob  upload  download
//	str-replace  watch-list  watch-create  watch-events  watch-poll  watch-wait  watch-stop
//
// 约定：args[0] 为动作名；flags, pos := parseFlags(args[1:])；调用 SDK 后统一 printJSON(resp)。
// 动作 → 方法映射（《API-REFERENCE.md》§3.4）：
//
//	list→ListPath  read→ReadFile  write→WriteFile  replace→ReplaceInFile  search→SearchInFile
//	find→FindFiles  grep→GrepFiles  glob→GlobFiles  upload→UploadFile  download→DownloadFile
//	str-replace→StrReplaceEditor
//	watch-list→WatchList  watch-create→WatchCreate  watch-events→WatchEvents
//	watch-poll→WatchPoll  watch-wait→WatchWait  watch-stop→WatchStop

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

// cmdFile 是 `file` 命名空间入口：cmdFile(c, args)，args[0] 为动作名。
func cmdFile(c *client.Client, args []string) {
	const usageText = `sandbox-sdk-go file <动作> [参数]

  list         <路径> [--recursive] [--show-hidden] [--file-types=.py,.txt] [--max-depth=N] [--include-size] [--include-permissions] [--sort-by=name|size|modified|type] [--sort-desc]
  read         <文件> [--start=N] [--end=N] [--sudo]
  write        <文件> [内容|--content=...|stdin] [--append] [--encoding=utf-8|base64|raw] [--leading-newline] [--trailing-newline] [--sudo]
  replace      <文件> <旧文本> <新文本> [--sudo]
  search       <文件> <正则> [--sudo]
  find         <路径> <文件名glob>
  grep         <路径> <模式> [--include=*.py,*.ts] [--exclude=...] [--case-insensitive] [--fixed-strings] [--context-before=N] [--context-after=N] [--max-results=N] [--max-file-size=1M] [--multiline] [--offset=N] [--type=py] [--recursive]
  glob         <路径> <pattern> [--exclude=...] [--include-hidden] [--files-only] [--include-metadata] [--max-results=N] [--sort-by=...] [--sort-desc]
  upload       <本地文件> <远端路径>
  download     <远端路径> <本地文件>
  str-replace  view|create|replace|insert|undo-edit <路径> ...（详见各自 --help 用法行）
  watch-list
  watch-create <路径> [--recursive] [--exclude=a,b] [--debounce=N] [--include-patterns=*.go,*.md]
  watch-events <watcherId>
  watch-poll   <watcherId> [--cursor=N] [--limit=N] [--timeout=N]
  watch-wait   <路径> [--timeout=N] [--event-types=create,write]
  watch-stop   <watcherId>
`

	// 兼容调度层误传命名空间名（args=[file,...]）的情况；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "file" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	action := args[0]
	flags, pos := parseFlags(args[1:])

	// need 校验位置参数个数；不足则打印该动作用法并退出。
	need := func(n int, usage string) {
		if len(pos) < n {
			fatal("用法: %s", usage)
		}
	}
	// opt 取可选字符串选项（缺省 nil 指针），供 *string 字段使用。
	opt := func(k string) *string {
		if v := fget(flags, k, ""); v != "" {
			return sandboxsdkgo.String(v)
		}
		return nil
	}
	// contentArg 解析文本内容：位置参数 > --file-text > --content > 管道 stdin。
	contentArg := func(idx int, usage string) string {
		if len(pos) > idx {
			return pos[idx]
		}
		if v := fget(flags, "file-text", ""); v != "" {
			return v
		}
		if v := fget(flags, "content", ""); v != "" {
			return v
		}
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fatal("读取 stdin: %v", err)
			}
			return string(data)
		}
		fatal("用法: %s", usage)
		return ""
	}
	// fslice 解析逗号分隔的整数列表选项（如 --view-range=1,10）。
	fslice := func(k string) []int {
		parts := flist(flags, k)
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				fatal("file: --%s 需要逗号分隔的整数列表", k)
			}
			out = append(out, n)
		}
		return out
	}

	switch action {

	case "list": // ListPath
		need(1, "sandbox-sdk-go file list <路径> [--recursive] [--show-hidden] [--file-types=.py,.txt] [--max-depth=N] [--include-size] [--include-permissions] [--sort-by=name|size|modified|type] [--sort-desc]")
		resp, err := c.File.ListPath(ctx, &sandboxsdkgo.FileListRequest{
			Path:               pos[0],
			Recursive:          fbool(flags, "recursive"),
			ShowHidden:         fbool(flags, "show-hidden"),
			FileTypes:          flist(flags, "file-types"),
			MaxDepth:           fint(flags, "max-depth"),
			IncludeSize:        fbool(flags, "include-size"),
			IncludePermissions: fbool(flags, "include-permissions"),
			SortBy:             opt("sort-by"),
			SortDesc:           fbool(flags, "sort-desc"),
		})
		if err != nil {
			fatal("file list: %v", err)
		}
		printJSON(resp)

	case "read": // ReadFile
		need(1, "sandbox-sdk-go file read <文件> [--start=N] [--end=N] [--sudo]")
		resp, err := c.File.ReadFile(ctx, &sandboxsdkgo.FileReadRequest{
			File:      pos[0],
			StartLine: fint(flags, "start"),
			EndLine:   fint(flags, "end"),
			Sudo:      fbool(flags, "sudo"),
		})
		if err != nil {
			fatal("file read: %v", err)
		}
		printJSON(resp)

	case "write": // WriteFile
		need(1, "sandbox-sdk-go file write <文件> [内容|--content=...|stdin] [--append] [--encoding=utf-8|base64|raw] [--leading-newline] [--trailing-newline] [--sudo]")
		req := &sandboxsdkgo.FileWriteRequest{
			File:            pos[0],
			Content:         contentArg(1, "sandbox-sdk-go file write <文件> [内容|--content=...|stdin] [--append] [--encoding=...] [--sudo]"),
			Append:          fbool(flags, "append"),
			LeadingNewline:  fbool(flags, "leading-newline"),
			TrailingNewline: fbool(flags, "trailing-newline"),
			Sudo:            fbool(flags, "sudo"),
		}
		if enc := fget(flags, "encoding", ""); enc != "" {
			e := sandboxsdkgo.FileContentEncoding(enc)
			req.Encoding = &e
		}
		resp, err := c.File.WriteFile(ctx, req)
		if err != nil {
			fatal("file write: %v", err)
		}
		printJSON(resp)

	case "replace": // ReplaceInFile
		need(3, "sandbox-sdk-go file replace <文件> <旧文本> <新文本> [--sudo]")
		resp, err := c.File.ReplaceInFile(ctx, &sandboxsdkgo.FileReplaceRequest{
			File:   pos[0],
			OldStr: pos[1],
			NewStr: pos[2],
			Sudo:   fbool(flags, "sudo"),
		})
		if err != nil {
			fatal("file replace: %v", err)
		}
		printJSON(resp)

	case "search": // SearchInFile
		need(2, "sandbox-sdk-go file search <文件> <正则> [--sudo]")
		resp, err := c.File.SearchInFile(ctx, &sandboxsdkgo.FileSearchRequest{
			File:  pos[0],
			Regex: pos[1],
			Sudo:  fbool(flags, "sudo"),
		})
		if err != nil {
			fatal("file search: %v", err)
		}
		printJSON(resp)

	case "find": // FindFiles
		need(2, "sandbox-sdk-go file find <路径> <文件名glob>")
		resp, err := c.File.FindFiles(ctx, &sandboxsdkgo.FileFindRequest{
			Path: pos[0],
			Glob: pos[1],
		})
		if err != nil {
			fatal("file find: %v", err)
		}
		printJSON(resp)

	case "grep": // GrepFiles
		need(2, "sandbox-sdk-go file grep <路径> <模式> [--include=*.py,*.ts] [--exclude=...] [--case-insensitive] [--fixed-strings] [--context-before=N] [--context-after=N] [--max-results=N] [--max-file-size=1M] [--multiline] [--offset=N] [--type=py] [--recursive]")
		resp, err := c.File.GrepFiles(ctx, &sandboxsdkgo.FileGrepRequest{
			Path:            pos[0],
			Pattern:         pos[1],
			Include:         flist(flags, "include"),
			Exclude:         flist(flags, "exclude"),
			CaseInsensitive: fbool(flags, "case-insensitive"),
			FixedStrings:    fbool(flags, "fixed-strings"),
			ContextBefore:   fint(flags, "context-before"),
			ContextAfter:    fint(flags, "context-after"),
			MaxResults:      fint(flags, "max-results"),
			MaxFileSize:     opt("max-file-size"),
			Multiline:       fbool(flags, "multiline"),
			Offset:          fint(flags, "offset"),
			Type:            opt("type"),
			Recursive:       fbool(flags, "recursive"),
		})
		if err != nil {
			fatal("file grep: %v", err)
		}
		printJSON(resp)

	case "glob": // GlobFiles
		need(2, "sandbox-sdk-go file glob <路径> <pattern> [--exclude=...] [--include-hidden] [--files-only] [--include-metadata] [--max-results=N] [--sort-by=...] [--sort-desc]")
		resp, err := c.File.GlobFiles(ctx, &sandboxsdkgo.FileGlobRequest{
			Path:            pos[0],
			Pattern:         pos[1],
			Exclude:         flist(flags, "exclude"),
			IncludeHidden:   fbool(flags, "include-hidden"),
			FilesOnly:       fbool(flags, "files-only"),
			IncludeMetadata: fbool(flags, "include-metadata"),
			MaxResults:      fint(flags, "max-results"),
			SortBy:          opt("sort-by"),
			SortDesc:        fbool(flags, "sort-desc"),
		})
		if err != nil {
			fatal("file glob: %v", err)
		}
		printJSON(resp)

	case "upload": // UploadFile（multipart 流式）
		need(2, "sandbox-sdk-go file upload <本地文件> <远端路径>")
		f, err := os.Open(pos[0])
		if err != nil {
			fatal("file upload: 打开本地文件: %v", err)
		}
		defer f.Close()
		resp, err := c.File.UploadFile(ctx, &sandboxsdkgo.BodyUploadFile{
			File: f,
			Path: sandboxsdkgo.String(pos[1]),
		})
		if err != nil {
			fatal("file upload: %v", err)
		}
		printJSON(resp)

	case "download": // DownloadFile（返回 io.Reader，落到本地文件）
		need(2, "sandbox-sdk-go file download <远端路径> <本地文件>")
		rc, err := c.File.DownloadFile(ctx, &sandboxsdkgo.FileDownloadFileRequest{Path: pos[0]})
		if err != nil {
			fatal("file download: %v", err)
		}
		out, err := os.Create(pos[1])
		if err != nil {
			fatal("file download: 创建本地文件: %v", err)
		}
		n, err := io.Copy(out, rc)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fatal("file download: %v", err)
		}
		printJSON(map[string]any{"success": true, "file": pos[0], "local": pos[1], "bytes": n})

	case "str-replace": // StrReplaceEditor
		need(2, "sandbox-sdk-go file str-replace view|create|replace|insert|undo-edit <路径> ...")
		req := &sandboxsdkgo.StrReplaceEditorRequest{Path: pos[1]}
		switch pos[0] {
		case "view":
			req.Command = sandboxsdkgo.CommandView
			req.ViewRange = fslice("view-range")
			req.PageRange = fslice("page-range")
			req.RowRange = fslice("row-range")
			req.SlideRange = fslice("slide-range")
			req.SheetName = opt("sheet-name")
			req.EnableMetadata = fbool(flags, "enable-metadata")
		case "create":
			req.Command = sandboxsdkgo.CommandCreate
			req.FileText = sandboxsdkgo.String(contentArg(2, "sandbox-sdk-go file str-replace create <路径> [内容|--file-text=...|stdin]"))
		case "replace", "str_replace", "str-replace":
			need(4, "sandbox-sdk-go file str-replace replace <路径> <旧文本> <新文本> [--replace-mode=ALL|FIRST|LAST]")
			req.Command = sandboxsdkgo.CommandStrReplace
			req.OldStr = sandboxsdkgo.String(pos[2])
			req.NewStr = sandboxsdkgo.String(pos[3])
			if v := fget(flags, "replace-mode", ""); v != "" {
				switch strings.ToUpper(v) {
				case "ALL":
					m := sandboxsdkgo.StrReplaceEditorRequestReplaceModeAll
					req.ReplaceMode = &m
				case "FIRST":
					m := sandboxsdkgo.StrReplaceEditorRequestReplaceModeFirst
					req.ReplaceMode = &m
				case "LAST":
					m := sandboxsdkgo.StrReplaceEditorRequestReplaceModeLast
					req.ReplaceMode = &m
				default:
					fatal("file str-replace: --replace-mode 只支持 ALL|FIRST|LAST")
				}
			}
		case "insert":
			req.Command = sandboxsdkgo.CommandInsert
			if len(pos) > 2 {
				n, err := strconv.Atoi(pos[2])
				if err != nil {
					fatal("file str-replace insert: 行号需为整数: %v", err)
				}
				req.InsertLine = &n
			} else {
				req.InsertLine = fint(flags, "insert-line")
			}
			if req.InsertLine == nil {
				fatal("用法: sandbox-sdk-go file str-replace insert <路径> <行号> [新文本]")
			}
			if len(pos) > 3 {
				req.NewStr = sandboxsdkgo.String(pos[3])
			} else if fhas(flags, "new-str") {
				req.NewStr = sandboxsdkgo.String(fget(flags, "new-str", ""))
			}
		case "undo-edit", "undo_edit", "undo":
			req.Command = sandboxsdkgo.CommandUndoEdit
		default:
			fatal("用法: sandbox-sdk-go file str-replace view|create|replace|insert|undo-edit <路径> ...")
		}
		resp, err := c.File.StrReplaceEditor(ctx, req)
		if err != nil {
			fatal("file str-replace: %v", err)
		}
		printJSON(resp)

	case "watch-list": // WatchList
		resp, err := c.File.WatchList(ctx)
		if err != nil {
			fatal("file watch-list: %v", err)
		}
		printJSON(resp)

	case "watch-create": // WatchCreate
		need(1, "sandbox-sdk-go file watch-create <路径> [--recursive] [--exclude=a,b] [--debounce=N] [--include-patterns=*.go,*.md]")
		resp, err := c.File.WatchCreate(ctx, &sandboxsdkgo.CreateWatchRequest{
			Path:            pos[0],
			Recursive:       fbool(flags, "recursive"),
			Exclude:         flist(flags, "exclude"),
			Debounce:        fint(flags, "debounce"),
			IncludePatterns: flist(flags, "include-patterns"),
		})
		if err != nil {
			fatal("file watch-create: %v", err)
		}
		printJSON(resp)

	case "watch-events": // WatchEvents
		need(1, "sandbox-sdk-go file watch-events <watcherId>")
		resp, err := c.File.WatchEvents(ctx, pos[0])
		if err != nil {
			fatal("file watch-events: %v", err)
		}
		printJSON(resp)

	case "watch-poll": // WatchPoll
		need(1, "sandbox-sdk-go file watch-poll <watcherId> [--cursor=N] [--limit=N] [--timeout=N]")
		resp, err := c.File.WatchPoll(ctx, pos[0], &sandboxsdkgo.PollRequest{
			Cursor:  fint(flags, "cursor"),
			Limit:   fint(flags, "limit"),
			Timeout: fint(flags, "timeout"),
		})
		if err != nil {
			fatal("file watch-poll: %v", err)
		}
		printJSON(resp)

	case "watch-wait": // WatchWait
		need(1, "sandbox-sdk-go file watch-wait <路径> [--timeout=N] [--event-types=create,write]")
		var evs []sandboxsdkgo.AppSchemasFileWatchWaitRequestEventTypesItem
		for _, e := range flist(flags, "event-types") {
			evs = append(evs, sandboxsdkgo.AppSchemasFileWatchWaitRequestEventTypesItem(e))
		}
		resp, err := c.File.WatchWait(ctx, &sandboxsdkgo.FileWatchWaitRequest{
			Path:       pos[0],
			Timeout:    fint(flags, "timeout"),
			EventTypes: evs,
		})
		if err != nil {
			fatal("file watch-wait: %v", err)
		}
		printJSON(resp)

	case "watch-stop": // WatchStop
		need(1, "sandbox-sdk-go file watch-stop <watcherId>")
		resp, err := c.File.WatchStop(ctx, pos[0])
		if err != nil {
			fatal("file watch-stop: %v", err)
		}
		printJSON(resp)

	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}
