package main

// cmd_env.go —— cubesandbox-sdk-go 的"一条指令推送变量"。
//
// 与 agent-infra 侧的差别（重要）：
//   官方 CubeSandbox SDK 的执行接口（envd Process API）**原生支持 env 注入**，
//   所以这里不需要往沙箱写文件 —— 只需记住"要注入哪些变量名"，
//   之后每条 exec 从**本地环境**现取值注入。
//   好处：密钥不落沙箱磁盘、不进程列表可见、沙箱销毁即彻底消失。
//
//	sandbox-sdk-go… 用法：
//	  cubesandbox-sdk-go envpush GITHUB_TOKEN GH_ORG     # 记住这些变量名
//	  cubesandbox-sdk-go exec <sid> 'gh auth status'     # 自动带上
//	  cubesandbox-sdk-go envpush --list                  # 查看已登记
//	  cubesandbox-sdk-go envpush --clear                 # 清空登记

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const envStateRel = ".cubesandbox-sdk-go/envs"

func envStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/root"
	}
	return filepath.Join(home, envStateRel)
}

// cmdEnvPush 登记/查看/清空"要从本地注入沙箱的变量名"。
func cmdEnvPush(args []string) {
	path := envStatePath()
	rest := args[2:]   // args = [程序名, "envpush", 变量名...]

	if len(rest) == 0 {
		fatal(`用法:
  cubesandbox-sdk-go envpush NAME [NAME2 ...]   登记变量名（值留在本地，不落沙箱）
  cubesandbox-sdk-go envpush --list             查看已登记
  cubesandbox-sdk-go envpush --clear            清空登记
登记后，exec 会自动把这些变量注入沙箱内的命令环境。`)
	}

	switch rest[0] {
	case "--list", "-l":
		names := readEnvNames(path)
		if len(names) == 0 {
			fmt.Println("(未登记任何变量)")
			return
		}
		for _, n := range names {
			if v, ok := os.LookupEnv(n); ok {
				fmt.Printf("%-24s 本地已设置(len=%d)\n", n, len(v))
			} else {
				fmt.Printf("%-24s 本地未设置（注入时会被跳过）\n", n)
			}
		}
		return
	case "--clear", "-c":
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fatal("清空失败: %v", err)
		}
		fmt.Println("已清空登记")
		return
	}

	// 登记（去重、保持顺序）
	existing := readEnvNames(path)
	seen := map[string]bool{}
	for _, n := range existing {
		seen[n] = true
	}
	var added, missing []string
	for _, n := range rest {
		if !validEnvName(n) {
			fatal("非法变量名: %s", n)
		}
		if _, ok := os.LookupEnv(n); !ok {
			missing = append(missing, n)
		}
		if !seen[n] {
			seen[n] = true
			added = append(added, n)
		}
	}
	all := append(existing, added...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fatal("创建状态目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(all, "\n")+"\n"), 0o600); err != nil {
		fatal("写入登记失败: %v", err)
	}

	fmt.Printf("已登记 %d 个变量：%s\n", len(added), strings.Join(added, ", "))
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "[cubesandbox-sdk-go] 注意：本地未设置 %s，注入时会被跳过\n", strings.Join(missing, ", "))
	}
	fmt.Println("之后的 exec 会自动注入；值只存在本地，不写入沙箱磁盘。")
	fmt.Printf("状态文件：%s\n", path)
}

// readEnvNames 读取已登记的变量名。
func readEnvNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pushedEnvs 把已登记的变量名解析为"当前本地环境里的值"。
// 每次调用现取，所以本地改了值立刻生效；本地没有的变量直接跳过。
func pushedEnvs() map[string]string {
	names := readEnvNames(envStatePath())
	if len(names) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok && v != "" {
			out[n] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeEnvs 合并显式 --env 与已登记变量（显式优先）。
func mergeEnvs(explicit map[string]string) map[string]string {
	pushed := pushedEnvs()
	if len(explicit) == 0 && len(pushed) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range pushed {
		out[k] = v
	}
	for k, v := range explicit {
		out[k] = v
	}
	return out
}

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
