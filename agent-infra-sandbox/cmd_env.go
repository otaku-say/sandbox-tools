package main

// cmd_env.go —— 把本地环境变量"一条指令"推送到沙箱，并让之后的命令自动带上。
//
//	sandbox-sdk-go envpush NAME [NAME2 ...]        从本地环境读取这些变量
//	sandbox-sdk-go envpush NAME=value [NAME2=...]  显式指定值
//
// 实现：写入沙箱 ~/.env.auto（内容只走文件通道，不进命令行），
// 再在 ~/.bash_profile 里挂一段受标记保护的 source 块；
// 因为 AIO 的 Bash.Exec 用 `bash -l -c`（login shell），之后所有 exec/run/job 自动生效。

import (
	"fmt"
	"os"
	"strings"

	sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"
	"github.com/agent-infra/sandbox-sdk-go/client"
)

const (
	envAutoPath    = "/home/gem/.env.auto"
	envProfilePath = "/home/gem/.bash_profile"
	envMarkBegin   = "# >>> sandbox-sdk-go envpush >>>"
	envMarkEnd     = "# <<< sandbox-sdk-go envpush <<<"
)

func cmdEnvPush(c *client.Client, args []string) {
	rest := args[2:]
	if len(rest) == 0 {
		fatal(`用法:
  sandbox-sdk-go envpush NAME [NAME2 ...]        从本地环境读取
  sandbox-sdk-go envpush NAME=value [NAME2=...]  显式指定值
例: sandbox-sdk-go envpush GITHUB_TOKEN GH_ORG`)
	}

	var lines []string
	var names []string
	for _, a := range rest {
		name, val := a, ""
		if i := strings.Index(a, "="); i > 0 {
			name, val = a[:i], a[i+1:]
		} else {
			val = os.Getenv(name)
			if val == "" {
				fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 跳过 %s：本地环境里没有该变量\n", name)
				continue
			}
		}
		if !validEnvName(name) {
			fatal("非法的变量名: %s", name)
		}
		lines = append(lines, fmt.Sprintf("export %s=%s", name, shellQuote(val)))
		names = append(names, fmt.Sprintf("%s(len=%d)", name, len(val)))
	}
	if len(lines) == 0 {
		fatal("没有可推送的变量（本地为空且未显式给值）")
	}
	body := strings.Join(lines, "\n") + "\n"

	// 1) 写入 .env.auto（内容走文件通道，不出现在命令行里）
	if _, err := c.File.WriteFile(ctx, &sandboxsdkgo.FileWriteRequest{
		File:    envAutoPath,
		Content: body,
	}); err != nil {
		fatal("写入 %s 失败: %v", envAutoPath, err)
	}

	// 2) 在 ~/.bash_profile 挂一个受标记保护的 source 块（幂等）
	block := envMarkBegin + "\n[ -f " + envAutoPath + " ] && . " + envAutoPath + "\n" + envMarkEnd + "\n"
	existing := ""
	if resp, err := c.File.ReadFile(ctx, &sandboxsdkgo.FileReadRequest{File: envProfilePath}); err == nil && resp != nil && resp.Data != nil {
		existing = resp.Data.Content // FileReadResult.Content 是 string
	}
	cleaned := stripEnvBlock(existing)
	if cleaned != "" && !strings.HasSuffix(cleaned, "\n") {
		cleaned += "\n"
	}
	if _, err := c.File.WriteFile(ctx, &sandboxsdkgo.FileWriteRequest{
		File:    envProfilePath,
		Content: cleaned + block,
	}); err != nil {
		fatal("写入 %s 失败: %v", envProfilePath, err)
	}

	// 3) 收紧权限（密钥文件不该让同机其他用户读到）
	if _, err := c.Bash.Exec(ctx, &sandboxsdkgo.BashExecRequest{
		Command: fmt.Sprintf("chmod 600 %s %s", envAutoPath, envProfilePath),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[sandbox-sdk-go] 提示：chmod 失败（不影响功能）: %v\n", err)
	}

	fmt.Printf("已推送 %d 个变量到沙箱: %s\n", len(names), strings.Join(names, ", "))
	fmt.Printf("存放于 %s（600），并在 %s 挂上自动加载块。\n", envAutoPath, envProfilePath)
	fmt.Println("之后的 exec / run / job 会自动带上这些变量，可直接验证：")
	fmt.Println("  sandbox-sdk-go exec 'echo ${#GITHUB_TOKEN}'")
	fmt.Printf("清除：sandbox-sdk-go exec 'rm -f %s' 并删除 %s 中的 %s 段\n", envAutoPath, envProfilePath, envMarkBegin)
}

// validEnvName 只允许 POSIX 变量名。
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// stripEnvBlock 移除上一次写入的标记块（保证重复推送幂等）。
func stripEnvBlock(s string) string {
	for {
		i := strings.Index(s, envMarkBegin)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], envMarkEnd)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len(envMarkEnd):]
	}
}
