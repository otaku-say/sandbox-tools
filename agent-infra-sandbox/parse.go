package main

import (
	"fmt"
	"strings"
)

// 统一参数约定：选项写成 --key=value（布尔写 --flag），其余为位置参数。
// 这样命令里的 -n 之类不会被误吞（与 cubesandbox-sdk-go 的 splitArgs 语义一致）。

// parseFlags 拆分选项与位置参数。
func parseFlags(args []string) (map[string]string, []string) {
	flags := map[string]string{}
	var pos []string
	for _, a := range args {
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			name := a[2:]
			if i := strings.Index(name, "="); i >= 0 {
				flags[name[:i]] = name[i+1:]
			} else {
				flags[name] = "true"
			}
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos
}

// fget 取字符串选项（缺省用 def）。
func fget(f map[string]string, k, def string) string {
	if v, ok := f[k]; ok && v != "" {
		return v
	}
	return def
}

// fhas 选项是否存在且为真。
func fhas(f map[string]string, k string) bool {
	v, ok := f[k]
	return ok && v != "" && v != "false" && v != "0"
}

// fint 取整数选项，缺省返回 nil。
func fint(f map[string]string, k string) *int {
	if v, ok := f[k]; ok && v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return &n
		}
	}
	return nil
}

// ffloat 取浮点选项。
func ffloat(f map[string]string, k string) *float64 {
	if v, ok := f[k]; ok && v != "" {
		var x float64
		if _, err := fmt.Sscanf(v, "%f", &x); err == nil {
			return &x
		}
	}
	return nil
}

// fbool 取布尔选项（--x / --x=true / --x=1）。
func fbool(f map[string]string, k string) *bool {
	if v, ok := f[k]; ok {
		b := v == "true" || v == "1" || v == ""
		return &b
	}
	return nil
}

// flist 取逗号分隔的列表选项。
func flist(f map[string]string, k string) []string {
	if v, ok := f[k]; ok && v != "" {
		parts := strings.Split(v, ",")
		out := parts[:0]
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				out = append(out, strings.TrimSpace(p))
			}
		}
		return out
	}
	return nil
}
