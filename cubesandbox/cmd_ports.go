package main

// ports —— 实测沙箱内**实际监听**的端口（模板声明的 exposedPorts 不一定准）。
//
// 为什么不能信模板声明（均本部署实测）：
//   - 创建沙箱时 body 里带 exposedPorts，平台返回 201 但**静默忽略**该字段；
//     未声明的端口照样能经 /sandbox/<id>/<port>/ 从外部访问（实测未声明的 9999 → 200）
//   - 反向也不成立：模板声明了 18091，但 aio-daemon 镜像里根本没服务在听
//   - 真正决定“能不能远程连”的就两条：
//       ① 沙箱内进程确实在监听该端口
//       ② 绑定地址非 loopback —— 绑 127.0.0.1 的服务外部访问会 502（实测）
// 所以本命令直接读 /proc/net/tcp*，并与模板声明做对比。

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// listenPort 一个实际监听中的端口。
type listenPort struct {
	Port     int    `json:"port"`
	Proto    string `json:"proto"` // tcp / tcp6
	Bind     string `json:"bind"`  // 0.0.0.0 / 127.0.0.1 / :: / 具体 IP
	External bool   `json:"external"`
	Name     string `json:"name,omitempty"` // 已知服务名（来自 portCatalog）
	Declared bool   `json:"declared"`       // 模板是否声明了它
	URL      string `json:"url,omitempty"`  // 外部可访问时的完整 URL
}

func hexByte(s string) int {
	n, _ := strconv.ParseUint(s, 16, 16)
	return int(n)
}

// bindFromHex 把 /proc/net/tcp* 的十六进制绑定地址转成可读形式，并判断能否从外部访问。
func bindFromHex(proto, hexAddr string) (string, bool) {
	h := strings.ToUpper(hexAddr)
	if strings.Trim(h, "0") == "" {
		if proto == "tcp6" {
			return "::", true // 双栈全地址
		}
		return "0.0.0.0", true
	}
	// IPv4：小端 32 位，如 0100007F = 127.0.0.1
	if proto == "tcp" && len(h) == 8 {
		ip := fmt.Sprintf("%d.%d.%d.%d",
			hexByte(h[6:8]), hexByte(h[4:6]), hexByte(h[2:4]), hexByte(h[0:2]))
		return ip, !strings.HasPrefix(ip, "127.")
	}
	// IPv6：::1 = 0000...01000000
	if len(h) == 32 && strings.Trim(h[:24], "0") == "" && h[24:] == "01000000" {
		return "::1", false
	}
	return hexAddr, true
}

// cmdPorts 用法: ports <sid> [--json]
func cmdPorts(c *cubesandbox.Client, args []string) {
	flags, sid, rest := splitArgs(args)
	if sid == "" && len(rest) > 0 {
		sid = rest[0]
	}
	need(sid != "", "用法: ports <sid> [--json]")
	sb := connect(c, sid)

	// 读 /proc/net/tcp*（st == 0A 即 LISTEN）
	cmd := `for f in /proc/net/tcp /proc/net/tcp6; do
  [ -r "$f" ] || continue
  p=$(basename "$f")
  awk 'NR>1 && $4=="0A" {print $2}' "$f" | while read -r a; do echo "$p ${a%:*} ${a##*:}"; done
done`
	res, err := sb.Commands().Run(ctx, cmd, cubesandbox.CommandOptions{Timeout: 30 * time.Second})
	if err != nil {
		fatal("读取沙箱监听端口失败: %v", err)
	}

	seen := map[int]listenPort{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pn, perr := strconv.ParseUint(f[2], 16, 16)
		if perr != nil {
			continue
		}
		port := int(pn)
		bind, ext := bindFromHex(f[0], f[1])
		if old, ok := seen[port]; ok && old.External {
			continue // 同一端口已有“更可达”的记录，保留它
		}
		seen[port] = listenPort{Port: port, Proto: f[0], Bind: bind, External: ext}
	}

	// 取该沙箱的模板，拿声明端口做对比
	tplID := ""
	if list, lerr := c.List(ctx); lerr == nil {
		for _, s := range list {
			if s.SandboxID == sid {
				tplID = s.TemplateID
				break
			}
		}
	}
	declared := map[int]bool{}
	if tplID != "" {
		if d, derr := templateDetail(tplID); derr == nil {
			for _, p := range parsePortSpec(d.Ports) {
				declared[p] = true
			}
		}
	}

	proxy := strings.TrimSuffix(envPick("", "CUBESANDBOX_PROXY_URL", "CBS_PROXY_BASE"), "/")
	ports := make([]listenPort, 0, len(seen))
	for _, v := range seen {
		v.Declared = declared[v.Port]
		if sp, ok := portCatalog[v.Port]; ok {
			v.Name = sp.Name
		}
		if v.External && proxy != "" {
			v.URL = fmt.Sprintf("%s/sandbox/%s/%d/", proxy, sid, v.Port)
		}
		ports = append(ports, v)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })

	if flags["json"] == "true" {
		printJSON(ports)
		return
	}

	fmt.Fprintf(os.Stderr, "沙箱 %s  模板 %s\n", sid, tplID)
	fmt.Printf("%-6s %-16s %-6s %-18s %-8s %s\n", "端口", "绑定", "外部", "服务", "模板声明", "访问地址")
	for _, p := range ports {
		ext := "-"
		url := "（仅沙箱内部）"
		if p.External {
			ext = "可连"
			if p.URL != "" {
				url = p.URL
			}
		}
		dec := "否"
		if p.Declared {
			dec = "是"
		}
		name := p.Name
		if name == "" {
			name = "unknown"
		}
		fmt.Printf("%-6d %-16s %-6s %-18s %-8s %s\n", p.Port, p.Bind, ext, name, dec, url)
	}
	// 声明了却没在监听 —— 模板声明不可信的直观证据
	missing := []string{}
	for p := range declared {
		if _, ok := seen[p]; !ok {
			missing = append(missing, strconv.Itoa(p))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(os.Stderr, "提示：模板声明了但沙箱内**没有监听**的端口：%s\n", strings.Join(missing, ", "))
	}
}
