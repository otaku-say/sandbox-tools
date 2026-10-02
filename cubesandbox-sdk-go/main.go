// cubesandbox-sdk-go —— CubeSandbox 遥控 CLI（基于官方 Go SDK：github.com/tencentcloud/CubeSandbox/sdk/go）
//
// 为什么需要它：
//   官方 SDK 的数据面走 e2b 风格虚拟域名 <port>-<sandboxID>.cube.app，
//   在没有 DNS / 只能经 Cloudflare 路径式代理访问源站的环境里（本机 iSH 就是），
//   直连数据面会被源站防火墙拒绝。
//   本工具注入自定义 RoundTripper，把数据面请求改写成 CF 路径式路由：
//       https://<proxy>/sandbox/<sandboxID>/<port>/<path>
//   控制面请求（CubeAPI 域名）原样透传。
//
// 用法：
//   cubesandbox-sdk-go new [--timeout 3600] [--note 任务名]
//   cubesandbox-sdk-go ls | cubesandbox-sdk-go info <sid> | cubesandbox-sdk-go rm <sid>
//   cubesandbox-sdk-go exec <sid> <命令...>
//   cubesandbox-sdk-go put <sid> <本地文件> <远端路径>
//   cubesandbox-sdk-go get <sid> <远端路径> <本地文件>
//   cubesandbox-sdk-go cat <sid> <远端路径>
//   cubesandbox-sdk-go health
//
// 环境变量：
//   CUBE_API_URL     CubeAPI 地址（默认本部署的 CF 域名）
//   CUBE_API_KEY     API 密钥
//   CUBE_TEMPLATE_ID 默认模板
//   CBS_PROXY_BASE   数据面 CF 代理基址（默认 https://<cubesandbox-proxy-host>）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

const (
	defAPIURL    = "https://<cubesandbox-api-host>"
	defProxyBase = "https://<cubesandbox-proxy-host>"
	defTemplate  = "tpl-59f34c49abc04d66a7002b84"
	version      = "1.0.0"
)

// 数据面 host 形如 8080-<32位hex>.cube.app
var dataHostRe = regexp.MustCompile(`^(\d+)-([0-9a-f]{32})\.`)

// cfTransport 把数据面请求改写成 CF 路径式路由，其余请求原样透传。
type cfTransport struct {
	base      http.RoundTripper
	proxyBase *url.URL
}

func (t *cfTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := dataHostRe.FindStringSubmatch(req.URL.Host)
	if m == nil {
		return t.base.RoundTrip(req) // 控制面 / 其它，原样
	}
	port, sid := m[1], m[2]
	u := *req.URL
	u.Scheme = t.proxyBase.Scheme
	u.Host = t.proxyBase.Host
	u.Path = strings.TrimSuffix(t.proxyBase.Path, "/") + "/sandbox/" + sid + "/" + port + req.URL.Path
	r2 := req.Clone(req.Context())
	r2.URL = &u
	r2.Host = ""
	r2.Header.Del("Host")
	return t.base.RoundTrip(r2)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newClient() (*cubesandbox.Client, error) {
	base := envOr("CBS_PROXY_BASE", defProxyBase)
	pu, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("CBS_PROXY_BASE 非法: %w", err)
	}
	cfg := cubesandbox.NewConfigFromEnv()
	cfg.APIURL = envOr("CUBE_API_URL", defAPIURL)
	cfg.APIKey = os.Getenv("CUBE_API_KEY")
	cfg.TemplateID = envOr("CUBE_TEMPLATE_ID", defTemplate)

	hc := &http.Client{Transport: &cfTransport{base: http.DefaultTransport, proxyBase: pu}}
	return cubesandbox.NewClient(cfg, cubesandbox.WithHTTPClient(hc)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `cubesandbox-sdk-go `+version+` —— CubeSandbox 遥控 CLI

  cubesandbox-sdk-go new [--timeout 秒] [--note 名称] [--template 模板ID]
  cubesandbox-sdk-go ls
  cubesandbox-sdk-go info <sid>
  cubesandbox-sdk-go rm <sid>
  cubesandbox-sdk-go exec <sid> <命令...>
  cubesandbox-sdk-go put <sid> <本地文件> <远端路径>
  cubesandbox-sdk-go get <sid> <远端路径> <本地文件>
  cubesandbox-sdk-go cat <sid> <远端路径>
  cubesandbox-sdk-go health
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	client, err := newClient()
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	ctx := context.Background()

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("cubesandbox-sdk-go", version)
		return
	case "health":
		h, err := client.Health(ctx)
		if err != nil {
			fatal(err)
		}
		printJSON(h)
		return
	case "new":
		fs := flag.NewFlagSet("new", flag.ExitOnError)
		timeout := fs.Int("timeout", 3600, "空闲超时秒数（-1=永不回收）")
		note := fs.String("note", "", "metadata note")
		tpl := fs.String("template", "", "模板 ID")
		fs.Parse(args)
		opts := cubesandbox.CreateOptions{}
		if *tpl != "" {
			opts.TemplateID = *tpl
		}
		if *timeout != 0 {
			d := time.Duration(*timeout) * time.Second
			opts.Timeout = &d
		}
		if *note != "" {
			opts.Metadata = map[string]string{"note": *note}
		}
		sb, err := client.Create(ctx, opts)
		if err != nil {
			fatal(err)
		}
		fmt.Println(sb.SandboxID)
		return
	case "ls":
		list, err := client.List(ctx)
		if err != nil {
			fatal(err)
		}
		printJSON(list)
		return
	}

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	sid := args[0]
	sb, err := client.Connect(ctx, sid)
	if err != nil {
		fatal(fmt.Errorf("连接沙箱 %s 失败: %w", sid, err))
	}

	switch cmd {
	case "info":
		info, err := sb.GetInfo(ctx)
		if err != nil {
			fatal(err)
		}
		printJSON(info)
	case "rm":
		if err := sb.Kill(ctx); err != nil {
			fatal(err)
		}
		fmt.Println("killed", sid)
	case "exec":
		if len(args) < 2 {
			fatal(fmt.Errorf("用法: cubesandbox-sdk-go exec <sid> <命令...>"))
		}
		res, err := sb.Commands().Run(ctx, strings.Join(args[1:], " "), cubesandbox.CommandOptions{})
		if err != nil {
			fatal(err)
		}
		if res.Stdout != "" {
			fmt.Print(res.Stdout)
		}
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
		if res.ExitCode != 0 {
			os.Exit(res.ExitCode)
		}
	case "put":
		if len(args) != 3 {
			fatal(fmt.Errorf("用法: cubesandbox-sdk-go put <sid> <本地文件> <远端路径>"))
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			fatal(err)
		}
		if err := sb.Files().Write(ctx, args[2], data); err != nil {
			fatal(err)
		}
		fmt.Printf("%s -> %s (%d 字节)\n", args[1], args[2], len(data))
	case "get":
		if len(args) != 3 {
			fatal(fmt.Errorf("用法: cubesandbox-sdk-go get <sid> <远端路径> <本地文件>"))
		}
		s, err := sb.Files().Read(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(args[2], []byte(s), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("%s -> %s (%d 字节)\n", args[1], args[2], len(s))
	case "cat":
		if len(args) != 2 {
			fatal(fmt.Errorf("用法: cubesandbox-sdk-go cat <sid> <远端路径>"))
		}
		s, err := sb.Files().Read(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		io.WriteString(os.Stdout, s)
	default:
		usage()
		os.Exit(2)
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(b))
}
