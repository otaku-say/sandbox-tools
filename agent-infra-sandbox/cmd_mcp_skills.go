// cmd_mcp_skills.go —— `mcp`（3 方法）与 `skills`（5 方法）两个命名空间的 CLI 实现。
//
//	mcp     servers | tools | call                    ← ListMcpServers / ListMcpTools / ExecuteMcpTool
//	skills  ls | content | register | rm | clear      ← ListMetadata / GetContent / RegisterSkills / DeleteSkill / ClearSkills
//
// 入口签名与 dispatch.go 一致：args[0] 为动作名（namespace 由调度层剥掉）。
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

// ---------- mcp ----------

// mcpUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func mcpUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go mcp <动作>

  servers                              列出已配置的 MCP server（ListMcpServers）
  tools   <server>                     列出该 server 提供的工具（ListMcpTools）
  call    <server> <tool> [argsJSON]   调用工具（ExecuteMcpTool；--args '<json对象>' 传参）
`)
	os.Exit(code)
}

func cmdMcp(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[mcp,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "mcp" {
		args = args[1:]
	}
	if len(args) == 0 {
		mcpUsage(os.Stdout, 0)
	}
	flags, pos := parseFlags(args[1:])
	switch args[0] {
	case "servers":
		resp, err := c.Mcp.ListMcpServers(ctx)
		check(err)
		printJSON(resp)
	case "tools":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go mcp tools <server>")
		}
		resp, err := c.Mcp.ListMcpTools(ctx, pos[0])
		check(err)
		printJSON(resp)
	case "call":
		if len(pos) < 2 {
			fatal("用法: sandbox-sdk-go mcp call <server> <tool> [argsJSON]")
		}
		raw := fget(flags, "args", "")
		if raw == "" && len(pos) > 2 {
			raw = pos[2]
		}
		payload := map[string]any{}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				fatal("工具参数必须是 JSON 对象: %v", err)
			}
			if payload == nil {
				payload = map[string]any{}
			}
		}
		resp, err := c.Mcp.ExecuteMcpTool(ctx, pos[0], pos[1], payload)
		check(err)
		printJSON(resp)
	default:
		mcpUsage(os.Stderr, 2)
	}
}

// ---------- skills ----------

// skillsUsage 输出用法并退出：code==0（只输入命名空间、查看动作清单）走 stdout，否则走 stderr。
func skillsUsage(out *os.File, code int) {
	fmt.Fprint(out, `用法: sandbox-sdk-go skills <动作>

  ls       [names...] [--names a,b]               列出技能元数据（ListMetadata）
  content  <name>                                 读取技能内容（GetContent）
  register <本地文件或目录> [--name N] [--path P]   注册技能（RegisterSkills；目录自动打包为 .tar.gz）
  rm       <name>                                 删除技能（DeleteSkill）
  clear                                           清空技能（ClearSkills）
`)
	os.Exit(code)
}

func cmdSkills(c *client.Client, args []string) {
	// 兼容调度层把命名空间名一并传入（args=[skills,...]）的形态；正常约定 args[0] 即动作名。
	if len(args) > 0 && args[0] == "skills" {
		args = args[1:]
	}
	if len(args) == 0 {
		skillsUsage(os.Stdout, 0)
	}
	flags, pos := parseFlags(args[1:])
	switch args[0] {
	case "ls":
		names := fget(flags, "names", "")
		if names == "" && len(pos) > 0 {
			names = strings.Join(pos, ",")
		}
		req := &sandboxsdkgo.SkillsListMetadataRequest{}
		if names != "" {
			req.Names = sandboxsdkgo.String(names)
		}
		resp, err := c.Skills.ListMetadata(ctx, req)
		check(err)
		printJSON(resp)
	case "content":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go skills content <name>")
		}
		resp, err := c.Skills.GetContent(ctx, pos[0])
		check(err)
		printJSON(resp)
	case "register":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go skills register <本地文件或目录> [--name N] [--path P]")
		}
		reader, filename, ctype, err := skillPayload(pos[0])
		check(err)
		req := &sandboxsdkgo.BodyRegisterSkills{
			File: sandboxsdkgo.NewFileParam(reader, filename, ctype),
		}
		if v := fget(flags, "name", ""); v != "" {
			req.Name = sandboxsdkgo.String(v)
		}
		if v := fget(flags, "path", ""); v != "" {
			req.Path = sandboxsdkgo.String(v)
		}
		resp, err := c.Skills.RegisterSkills(ctx, req)
		check(err)
		printJSON(resp)
	case "rm":
		if len(pos) < 1 {
			fatal("用法: sandbox-sdk-go skills rm <name>")
		}
		resp, err := c.Skills.DeleteSkill(ctx, pos[0])
		check(err)
		printJSON(resp)
	case "clear":
		resp, err := c.Skills.ClearSkills(ctx)
		check(err)
		printJSON(resp)
	default:
		skillsUsage(os.Stderr, 2)
	}
}

// skillPayload 读取待注册的技能内容：单文件原样上传，目录在内存中打包为 .tar.gz。
// 返回 (内容, 上传文件名, Content-Type, error)。
func skillPayload(path string) (io.Reader, string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", "", err
		}
		name := filepath.Base(path)
		ctype := "application/octet-stream"
		switch {
		case strings.HasSuffix(name, ".zip"):
			ctype = "application/zip"
		case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
			ctype = "application/gzip"
		}
		return bytes.NewReader(data), name, ctype, nil
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := filepath.Base(filepath.Clean(path))
	if root == "." || root == string(filepath.Separator) {
		root = "skill"
	}
	walkErr := filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = root + "/" + filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		} else if fi.Mode()&os.ModeSymlink != 0 {
			if link, lerr := os.Readlink(p); lerr == nil {
				hdr.Linkname = link
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, "", "", walkErr
	}
	if err := tw.Close(); err != nil {
		return nil, "", "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", "", err
	}
	return bytes.NewReader(buf.Bytes()), root + ".tar.gz", "application/gzip", nil
}
