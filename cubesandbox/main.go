// cubesandbox-sdk-go —— CubeSandbox 全功能遥控 CLI（基于官方 Go SDK）
//
// 原理：官方 SDK 的数据面走 e2b 风格虚拟域名 <port>-<sandboxID>.cube.app；
// 本工具在传输层注入 RoundTripper，把数据面请求改写成 CF 路径式路由：
//     https://<proxy>/sandbox/<sandboxID>/<port>/<path>
// 控制面请求（CubeAPI 域名）原样透传 —— 不需要改服务端任何配置。
//
// 覆盖官方 SDK 的完整能力面：沙箱生命周期、命令/代码执行、PTY 交互、
// 文件（读写/列/stat/改名/删除/watch）、快照/回滚/克隆、
// 持久卷 CRUD + 挂载、模板查询、网络策略、健康检查。
//
// 运行 `cubesandbox-sdk-go help` 查看全部命令。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

const (
	// 部署相关地址一律从环境变量读取——仓库内不写死任何私有域名：
	//   CUBESANDBOX_API_URL    控制面地址（例：https://<cubesandbox-api-host>）
	//   CUBESANDBOX_PROXY_URL  数据面网关地址（例：https://<cubesandbox-proxy-host>）
	defAPIURL    = ""
	defProxyBase = ""
	// 模板 ID 属于部署信息：仓库内不写死（用 CUBESANDBOX_TEMPLATE_ID 指定）
	defTemplate  = ""
	version      = "2.6.0"
)

var (
	dataHostRe = regexp.MustCompile(`^(\d+)-([0-9a-f]{32})\.`)
	ctx        = context.Background()
)

// ---------- 传输层：数据面 CF 路径改写 ----------

type cfTransport struct {
	base      http.RoundTripper
	proxyBase *url.URL
}

func (t *cfTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := dataHostRe.FindStringSubmatch(req.URL.Host)
	if m == nil {
		return t.base.RoundTrip(req)
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

// envPick 依次尝试多个环境变量名（新名在前、旧名兼容），返回第一个非空值；都没有时返回 def。
func envPick(def string, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return def
}

func newClient() *cubesandbox.Client {
	proxyBase := envPick(defProxyBase, "CUBESANDBOX_PROXY_URL", "CBS_PROXY_BASE")
	if proxyBase == "" {
		fatal("缺少 CUBESANDBOX_PROXY_URL：请设置数据面网关地址（例 https://<cubesandbox-proxy-host>）")
	}
	pu, err := url.Parse(proxyBase)
	if err != nil {
		fatal("CUBESANDBOX_PROXY_URL 非法: %v", err)
	}
	cfg := cubesandbox.NewConfigFromEnv()
	cfg.APIURL = envPick(defAPIURL, "CUBESANDBOX_API_URL", "CUBE_API_URL")
	if cfg.APIURL == "" {
		fatal("缺少 CUBESANDBOX_API_URL：请设置控制面地址（例 https://<cubesandbox-api-host>）")
	}
	cfg.APIKey = envPick("", "CUBESANDBOX_API_KEY", "CUBE_API_KEY")
	// 模板 ID 不再强制：new 会按 --template / --need / 动态挑选解析出具体 ID。
	cfg.TemplateID = envPick(defTemplate, "CUBESANDBOX_TEMPLATE_ID", "CUBE_TEMPLATE_ID")
	hc := &http.Client{Transport: &cfTransport{base: http.DefaultTransport, proxyBase: pu}}
	return cubesandbox.NewClient(cfg, cubesandbox.WithHTTPClient(hc))
}

// ---------- 小工具 ----------

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

func need(cond bool, format string, a ...any) {
	if !cond {
		fatal(format, a...)
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(string(b))
}

// splitArgs 把参数拆成：前置 flag（--k=v / --flag）、第一个位置参数（sid）、其余原样。
// 这样 `exec <sid> echo -n hi` 里的 -n 不会被当成 flag。
func splitArgs(args []string) (flags map[string]string, sid string, rest []string) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && sid == "" {
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				flags[name[:eq]] = name[eq+1:]
			} else {
				flags[name] = "true"
			}
			continue
		}
		if sid == "" {
			sid = a
			rest = args[i+1:]
			return
		}
	}
	return
}

// splitFlagsFrom 解析**任意位置**的已知 flag（--k=v 或 --k v），其余按原样保留。
// 用于 code / pty 这类"命令后面还能跟选项"的场景。
func splitFlagsFrom(args []string, known map[string]bool) (flags map[string]string, pos []string) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				if k := name[:eq]; known[k] {
					flags[k] = name[eq+1:]
					continue
				}
			} else if known[name] {
				if i+1 < len(args) {
					flags[name] = args[i+1]
					i++
				} else {
					flags[name] = "true"
				}
				continue
			}
		}
		pos = append(pos, a)
	}
	return
}

func dur(flags map[string]string, key string) time.Duration {
	v, ok := flags[key]
	if !ok || v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fatal("--%s 需要整数秒", key)
	}
	return time.Duration(n) * time.Second
}

func durPtr(flags map[string]string, key string) *time.Duration {
	d := dur(flags, key)
	if d == 0 {
		return nil
	}
	return &d
}

func envMap(flags map[string]string, key string) map[string]string {
	v, ok := flags[key]
	if !ok || v == "" {
		return nil
	}
	m := map[string]string{}
	for _, kv := range strings.Split(v, ",") {
		if i := strings.Index(kv, "="); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func connect(c *cubesandbox.Client, sid string) *cubesandbox.Sandbox {
	need(sid != "", "缺少 sandboxID")
	sb, err := c.Connect(ctx, sid)
	if err != nil {
		fatal("连接沙箱 %s 失败: %v", sid, err)
	}
	return sb
}

// ---------- 入口 ----------

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage()
		return
	}
	if cmd == "version" || cmd == "-v" || cmd == "--version" {
		fmt.Printf("cubesandbox-sdk-go %s\nCUBESANDBOX_API_URL=%s\nCUBESANDBOX_PROXY_URL=%s\n",
			version, envPick(defAPIURL, "CUBESANDBOX_API_URL", "CUBE_API_URL"), envPick(defProxyBase, "CUBESANDBOX_PROXY_URL", "CBS_PROXY_BASE"))
		return
	}
	// 不依赖网络的命令先行处理
	if cmd == "envpush" {
		cmdEnvPush(os.Args)
		return
	}
	c := newClient()
	switch cmd {
	// 沙箱
	case "new":
		cmdNew(c, args)
	case "ls":
		cmdList(c, args)
	case "info":
		cmdInfo(c, args)
	case "rm", "kill":
		cmdKill(c, args)
	case "pause":
		cmdPause(c, args)
	case "resume":
		cmdResume(c, args)
	case "timeout":
		cmdSetTimeout(c, args)
	case "net":
		cmdNetwork(c, args)
	// 执行
	case "exec":
		cmdExec(c, args)
	case "code":
		cmdCode(c, args)
	case "pty":
		cmdPty(c, args)
	// 文件
	case "ls-file":
		cmdLsFile(c, args)
	case "stat":
		cmdStat(c, args)
	case "cat":
		cmdCat(c, args)
	case "put":
		cmdPut(c, args)
	case "get":
		cmdGet(c, args)
	case "mkdir":
		cmdMkdir(c, args)
	case "rm-file":
		cmdRmFile(c, args)
	case "mv":
		cmdMv(c, args)
	case "exists":
		cmdExists(c, args)
	// 快照 / 克隆
	case "snap":
		cmdSnap(c, args)
	case "snap-ls":
		cmdSnapLs(c, args)
	case "snap-rm":
		cmdSnapRm(c, args)
	case "rollback":
		cmdRollback(c, args)
	case "clone":
		cmdClone(c, args)
	// 卷
	case "vol-ls":
		cmdVolLs(c, args)
	case "vol-new":
		cmdVolNew(c, args)
	case "vol-info":
		cmdVolInfo(c, args)
	case "vol-rm":
		cmdVolRm(c, args)
	// 模板 / 健康
	case "tpl-ls":
		cmdTplLs2(args)
	case "tpl-pick":
		cmdTplPick(args)
	case "tpl-caps":
		cmdTplCaps(c, args)
	case "tpl-from-image":
		cmdTplFromImage(args)
	case "tpl-info":
		cmdTplInfo(c, args)
	case "tpl-logs":
		cmdTplLogs(c, args)
	case "health":
		cmdHealth(c, args)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cubesandbox-sdk-go `+version+` —— CubeSandbox 全功能遥控 CLI

【沙箱生命周期】
  new [--timeout=秒] [--note=名称] [--template=ID|别名|镜像子串] [--need=shell,file,browser,desktop] [--env=K=V,...] [--vol=名字:路径[:ro],...] [--no-internet]
      # --template 最优先；否则 --need 按能力选（能力覆盖且资源最小）；再否则 CUBESANDBOX_TEMPLATE_ID → 动态挑选
  ls | info <sid> | rm <sid>
  pause <sid> [--wait] | resume <sid> [--timeout=秒] | timeout <sid> <秒>
  net <sid> [--no-internet] [--allow=域1,域2] [--deny=域1,域2]

【执行】
  exec <sid> <命令...>          [--cwd=目录] [--env=K=V] [--timeout=秒]
  code <sid> <代码>             [--lang=python|js|...]
  pty  <sid> [--cmd=/bin/bash]  [--cwd=目录]        # 交互式终端（转发 stdin/stdout）

【文件】
  ls-file <sid> <路径> | stat <sid> <路径> | exists <sid> <路径>
  cat <sid> <远端路径>                     # 文本内容
  put <sid> <本地文件> <远端路径>           # 上传（二进制安全）
  get <sid> <远端路径> <本地文件> [--binary] # 下载（--binary 走 base64，稳妥）
  mkdir <sid> <路径> | rm-file <sid> <路径> | mv <sid> <旧> <新>

【快照 / 回滚 / 克隆】
  snap <sid> [--name=名称] | snap-ls [--sandbox=sid] [--limit=N] | snap-rm <snapID>
  rollback <sid> <snapID>
  clone <sid> [-n=数量] [--concurrency=N]

【持久卷】
  vol-ls | vol-new <名字> [--driver=插件] | vol-info <卷ID> | vol-rm <卷ID>

【其它】
  envpush NAME [NAME2 ...]              登记要从本地注入沙箱的变量（exec 自动带上）
  tpl-ls [--json]                       列出模板（ID/别名/状态/CPU/内存/可写层/镜像）
  tpl-pick [--need=...] [--json]        打印选择结果（不建沙箱）
  tpl-caps [<模板ID>] [--probe] [--json] 模板能力表（--probe 真机探测并缓存）
  tpl-from-image <镜像> [--json|--curl|--create] [--alias=] [--cpu=] [--memory=] [--writable=] [--env=K=V,...]
                                        从镜像自带的标签读取模板默认值（端口/探针/可写层/CPU/内存/别名），
                                        --create 直接提交平台建模板；--curl 输出可直接执行的 curl
  tpl-info <模板ID> | tpl-logs <模板ID> <buildID>
  health | version | help

【模板动态选择】（不写死模板 ID）
  优先级：--template= → CUBESANDBOX_TEMPLATE_ID → 自动挑选
  自动挑选规则：
    ① CUBESANDBOX_TEMPLATE_PICK=子串1,子串2   按序匹配 别名/镜像/ID（命中即选）
    ② CUBESANDBOX_TEMPLATE_MIN_CPU=毫核、CUBESANDBOX_TEMPLATE_MIN_MEM=MiB   规格下限过滤
    ③ --need=shell,file,browser,desktop：能力覆盖需求 且 内存/CPU 最小者（可用 --template 覆盖）
    ④ 都没配：READY 里内存最小者（同则最新创建）
  能力来源：本地缓存 ~/.cubesandbox-sdk-go/caps.json（tpl-caps --probe 写入）→ 镜像名启发式

环境变量：CUBESANDBOX_API_URL、CUBESANDBOX_API_KEY、CUBESANDBOX_TEMPLATE_ID、CUBESANDBOX_PROXY_URL
（旧名 CUBE_API_URL / CBS_PROXY_BASE / CUBE_API_KEY / CUBE_TEMPLATE_ID 仍兼容）
`)
}
