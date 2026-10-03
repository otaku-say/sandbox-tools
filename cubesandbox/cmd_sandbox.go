package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// ---------------- 沙箱生命周期 ----------------

func cmdNew(c *cubesandbox.Client, args []string) {
	flags, _, _ := splitArgs(args)
	opts := cubesandbox.CreateOptions{}
	// 模板解析优先级：
	//   ① --template=<ID|别名|镜像子串>   ② --need=<能力列表>（能力覆盖 + 资源最小）
	//   ③ CUBESANDBOX_TEMPLATE_ID        ④ 缺省动态挑选（内存最小者）
	if v := flags["template"]; v != "" {
		opts.TemplateID = resolveTemplateID(v)
	} else if needs := flags["need"]; needs != "" {
		t, err := selectByNeed(parseNeeds(needs))
		if err != nil {
			fatal("按能力选择模板失败：%v", err)
		}
		opts.TemplateID = t.TemplateID
		fmt.Fprintf(os.Stderr, "[template] --need=%s → %s（%dm/%dMi）\n", needs, t.TemplateID, t.CPU, t.MemMB)
	} else {
		opts.TemplateID = resolveTemplateID("")
	}
	if d := durPtr(flags, "timeout"); d != nil {
		opts.Timeout = d
	}
	if v := flags["note"]; v != "" {
		opts.Metadata = map[string]string{"note": v}
	}
	if envs := envMap(flags, "env"); envs != nil {
		opts.EnvVars = envs
	}
	if v := flags["vol"]; v != "" {
		for _, spec := range strings.Split(v, ",") {
			p := strings.Split(spec, ":")
			need(len(p) >= 2, "--vol 格式：名字:挂载路径[:ro]")
			m := cubesandbox.VolumeMount{Name: p[0], Path: p[1]}
			if len(p) > 2 && p[2] == "ro" {
				m.ReadOnly = true
			}
			opts.VolumeMounts = append(opts.VolumeMounts, m)
		}
	}
	if flags["no-internet"] == "true" {
		f := false
		opts.AllowInternetAccess = &f
	}
	sb, err := c.Create(ctx, opts)
	if err != nil {
		fatal("创建沙箱失败: %v", err)
	}
	fmt.Println(sb.SandboxID)
	printEndpointsHint(sb.SandboxID, opts.TemplateID)
}

func cmdList(c *cubesandbox.Client, args []string) {
	flags, _, _ := splitArgs(args)
	list, err := c.List(ctx)
	if err != nil {
		fatal("列出沙箱失败: %v", err)
	}
	if flags["json"] == "true" {
		printJSON(list)
		return
	}
	if len(list) == 0 {
		fmt.Println("(无沙箱)")
		return
	}
	fmt.Printf("%-34s %-30s %s\n", "SANDBOX ID", "TEMPLATE", "ENDS")
	for _, s := range list {
		ends := "∞"
		if s.EndAt != nil {
			ends = time.Until(*s.EndAt).Round(time.Second).String()
		}
		fmt.Printf("%-34s %-30s %s\n", s.SandboxID, s.TemplateID, ends)
	}
}

func cmdInfo(c *cubesandbox.Client, args []string) {
	_, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	info, err := sb.GetInfo(ctx)
	if err != nil {
		fatal("获取信息失败: %v", err)
	}
	printJSON(info)
}

func cmdKill(c *cubesandbox.Client, args []string) {
	_, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	if err := sb.Kill(ctx); err != nil {
		fatal("销毁失败: %v", err)
	}
	fmt.Println("killed", sid)
}

func cmdPause(c *cubesandbox.Client, args []string) {
	flags, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	opts := cubesandbox.PauseOptions{}
	if flags["wait"] == "true" {
		t := true
		opts.Wait = &t
	}
	if err := sb.Pause(ctx, opts); err != nil {
		fatal("暂停失败: %v", err)
	}
	fmt.Println("paused", sid)
}

func cmdResume(c *cubesandbox.Client, args []string) {
	flags, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	if err := sb.Resume(ctx, durPtr(flags, "timeout")); err != nil {
		fatal("恢复失败: %v", err)
	}
	fmt.Println("resumed", sid)
}

func cmdSetTimeout(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: timeout <sid> <秒数>（-1=永不回收）")
	var secs int
	if _, err := fmt.Sscanf(rest[0], "%d", &secs); err != nil {
		fatal("秒数需为整数")
	}
	sb := connect(c, sid)
	if err := sb.SetTimeout(ctx, time.Duration(secs)*time.Second); err != nil {
		fatal("设置超时失败: %v", err)
	}
	fmt.Printf("%s timeout=%ds\n", sid, secs)
}

func cmdNetwork(c *cubesandbox.Client, args []string) {
	flags, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	var opts cubesandbox.UpdateNetworkOptions
	if v := flags["allow"]; v != "" {
		opts.AllowOut = strings.Split(v, ",")
	}
	if v := flags["deny"]; v != "" {
		opts.DenyOut = strings.Split(v, ",")
	}
	if flags["no-internet"] == "true" {
		f := false
		opts.AllowInternetAccess = &f
	}
	if err := sb.UpdateNetwork(ctx, opts); err != nil {
		fatal("更新网络策略失败: %v", err)
	}
	fmt.Println("network updated")
}

// ---------------- 执行 ----------------

func cmdExec(c *cubesandbox.Client, args []string) {
	flags, sid, rest := splitArgs(args)
	need(len(rest) > 0, "用法: exec <sid> <命令...>")
	sb := connect(c, sid)
	// 显式 --env 优先；再叠加 envpush 登记的变量（值从本地环境现取）
	opts := cubesandbox.CommandOptions{
		Cwd:     flags["cwd"],
		Envs:    mergeEnvs(envMap(flags, "env")),
		Timeout: dur(flags, "timeout"),
	}
	res, err := sb.Commands().Run(ctx, strings.Join(rest, " "), opts)
	if err != nil {
		fatal("执行失败: %v", err)
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
}

func cmdCode(c *cubesandbox.Client, args []string) {
	flags, pos := splitFlagsFrom(args, map[string]bool{"lang": true, "via": true, "timeout": true})
	need(len(pos) >= 2, "用法: code <sid> <代码> [--lang=python|js|bash] [--via=exec]")
	sid := pos[0]
	sb := connect(c, sid)
	code := strings.Join(pos[1:], " ")
	lang := flags["lang"]
	if lang == "" {
		lang = "python"
	}

	// 非 Python 语言，或显式 --via=exec 时，直接用 exec 跑对应解释器
	// （Jupyter 端口在部分部署只绑 127.0.0.1，且 kernel 未必覆盖各语言）
	if flags["via"] == "exec" || (lang != "python" && lang != "python3") {
		runCodeViaExec(sb, lang, code)
		return
	}

	ex, err := sb.RunCode(ctx, code, cubesandbox.RunCodeOptions{
		Language: lang,
		Timeout:  dur(flags, "timeout"),
	})
	if err != nil {
		runCodeViaExec(sb, lang, code) // RunCode 不可用时回退
		return
	}
	if ex.Text != "" {
		fmt.Print(ex.Text)
	}
	for _, l := range ex.Logs.Stdout {
		fmt.Println(l)
	}
	for _, l := range ex.Logs.Stderr {
		fmt.Fprintln(os.Stderr, l)
	}
	if ex.Error != nil {
		printJSON(ex.Error)
		os.Exit(1)
	}
}

// runCodeViaExec 用命令行解释器执行代码片段（-c / -e / bash -c）。
func runCodeViaExec(sb *cubesandbox.Sandbox, lang, code string) {
	var cmd string
	switch lang {
	case "python", "python3":
		cmd = "python3 -c " + shellQuote(code)
	case "js", "javascript", "node", "nodejs":
		cmd = "node -e " + shellQuote(code)
	case "bash", "sh", "shell":
		cmd = "bash -c " + shellQuote(code)
	default:
		fatal("不支持的语言: %s（可选 python / js / bash）", lang)
	}
	res, err := sb.Commands().Run(ctx, cmd, cubesandbox.CommandOptions{})
	if err != nil {
		fatal("执行失败: %v", err)
	}
	fmt.Print(res.Stdout)
	if res.Stderr != "" {
		fmt.Fprint(os.Stderr, res.Stderr)
	}
	os.Exit(res.ExitCode)
}

// cmdPty 打开交互式终端。带 --run="命令" 时执行命令并退出；否则转发 stdin/stdout。
func cmdPty(c *cubesandbox.Client, args []string) {
	flags, pos := splitFlagsFrom(args, map[string]bool{"run": true, "cwd": true, "rows": true, "cols": true, "timeout": true})
	need(len(pos) >= 1, "用法: pty <sid> [--run=\"命令\"] [--cwd=目录]")
	sid := pos[0]
	sb := connect(c, sid)
	size := cubesandbox.PtySize{Rows: 40, Cols: 120}
	if r := flags["rows"]; r != "" {
		fmt.Sscanf(r, "%d", &size.Rows)
	}
	if c := flags["cols"]; c != "" {
		fmt.Sscanf(c, "%d", &size.Cols)
	}
	h, err := sb.Pty().Create(ctx, size, cubesandbox.PtyCreateOptions{
		Cwd:     flags["cwd"],
		Timeout: dur(flags, "timeout"),
	})
	if err != nil {
		fatal("创建 PTY 失败: %v", err)
	}
	defer h.Disconnect()

	if run := flags["run"]; run != "" {
		// 等 shell 真正就绪，再发命令；PTY 需要回车符（\r）
		time.Sleep(1500 * time.Millisecond)
		if err := h.SendStdin(ctx, []byte(run+"\r")); err != nil {
			fatal("发送命令失败: %v", err)
		}
		time.Sleep(600 * time.Millisecond)
		if err := h.SendStdin(ctx, []byte("exit\r")); err != nil {
			fatal("发送 exit 失败: %v", err)
		}
	} else {
		// 交互转发：stdin → pty（读到 EOF 即停止）
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					_ = h.SendStdin(ctx, buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
	}
	code, err := h.Wait(func(b []byte) { _, _ = os.Stdout.Write(b) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[pty] %v\n", err)
	}
	os.Exit(code)
}
