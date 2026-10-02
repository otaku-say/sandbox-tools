package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// shellQuote 把路径安全地放进单引号 shell 字符串。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cmdLsFile(c *cubesandbox.Client, args []string) {
	flags, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: ls-file <sid> <路径>")
	sb := connect(c, sid)
	entries, err := sb.Files().List(ctx, rest[0])
	if err != nil {
		fatal("列目录失败: %v", err)
	}
	if flags["json"] == "true" {
		printJSON(entries)
		return
	}
	for _, e := range entries {
		fmt.Printf("%-10s %10d  %-8s %s\n", e.Type, e.Size, e.Permissions, e.Path)
	}
}

func cmdStat(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: stat <sid> <路径>")
	sb := connect(c, sid)
	st, err := sb.Files().Stat(ctx, rest[0])
	if err != nil {
		fatal("stat 失败: %v", err)
	}
	printJSON(st)
}

func cmdExists(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: exists <sid> <路径>")
	sb := connect(c, sid)
	ok, err := sb.Files().Exists(ctx, rest[0])
	if err != nil {
		fatal("检查失败: %v", err)
	}
	if ok {
		fmt.Println("exists")
	} else {
		fmt.Println("missing")
		os.Exit(1)
	}
}

func cmdCat(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: cat <sid> <远端路径>")
	sb := connect(c, sid)
	s, err := sb.Files().Read(ctx, rest[0])
	if err != nil {
		fatal("读取失败: %v", err)
	}
	fmt.Print(s)
}

func cmdPut(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 2, "用法: put <sid> <本地文件> <远端路径>")
	local, remote := rest[0], rest[1]
	data, err := os.ReadFile(local)
	if err != nil {
		fatal("读本地文件失败: %v", err)
	}
	sb := connect(c, sid)
	if err := sb.Files().Write(ctx, remote, data); err != nil {
		fatal("上传失败: %v", err)
	}
	fmt.Printf("%s -> %s (%d 字节)\n", local, remote, len(data))
}

// cmdGet 默认用 SDK 的文本读；--binary 时改用沙箱内 base64 再本地解码（二进制安全）。
func cmdGet(c *cubesandbox.Client, args []string) {
	flags, sid, rest := splitArgs(args)
	need(len(rest) >= 2, "用法: get <sid> <远端路径> <本地文件> [--binary]")
	remote, local := rest[0], rest[1]
	sb := connect(c, sid)

	var data []byte
	if flags["binary"] == "true" {
		res, err := sb.Commands().Run(ctx, "base64 -w0 "+shellQuote(remote), cubesandbox.CommandOptions{})
		if err != nil {
			fatal("读取失败: %v", err)
		}
		if res.ExitCode != 0 {
			fatal("沙箱内 base64 失败: %s", res.Stderr)
		}
		d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
		if err != nil {
			fatal("base64 解码失败: %v", err)
		}
		data = d
	} else {
		s, err := sb.Files().Read(ctx, remote)
		if err != nil {
			fatal("读取失败: %v（二进制文件请加 --binary）", err)
		}
		data = []byte(s)
	}
	if err := os.WriteFile(local, data, 0o644); err != nil {
		fatal("写本地文件失败: %v", err)
	}
	fmt.Printf("%s -> %s (%d 字节)\n", remote, local, len(data))
}

func cmdMkdir(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: mkdir <sid> <路径>")
	sb := connect(c, sid)
	e, err := sb.Files().MakeDir(ctx, rest[0])
	if err != nil {
		fatal("创建目录失败: %v", err)
	}
	fmt.Println("created", e.Path)
}

func cmdRmFile(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: rm-file <sid> <路径>")
	sb := connect(c, sid)
	if err := sb.Files().Remove(ctx, rest[0]); err != nil {
		fatal("删除失败: %v", err)
	}
	fmt.Println("removed", rest[0])
}

func cmdMv(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 2, "用法: mv <sid> <旧路径> <新路径>")
	sb := connect(c, sid)
	e, err := sb.Files().Rename(ctx, rest[0], rest[1])
	if err != nil {
		fatal("重命名失败: %v", err)
	}
	fmt.Println("renamed ->", e.Path)
}
