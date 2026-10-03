package main

// 模板能力模型：让调用方（Agent）按“需要什么能力”选模板，而不是记模板 ID。
//
//	简单编辑文件          → shell,file
//	Chrome 浏览器自动化    → shell,file,browser
//	远程桌面操控          → shell,file,browser,desktop
//
// 能力来源（优先级从高到低）：
//   1) 本地缓存 ~/.cubesandbox-sdk-go/caps.json（tpl-caps --probe 真机探测后写入；也可手工维护）
//   2) 镜像名启发式（仅 AIO 系：aio-computer→含 desktop；aio-*/aiod/all-in-one→含 browser；其余只给基线）
// 选择规则：在 READY 且**能力覆盖需求**的模板中，取**内存最小 → CPU 最小 → 创建最新**者。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// 能力关键字（--need= 的取值）
const (
	capShell   = "shell"
	capFile    = "file"
	capCode    = "code"
	capBrowser = "browser"
	capDesktop = "desktop"
)

// allCaps 用于校验 --need 拼写。
var allCaps = []string{capShell, capFile, capCode, capBrowser, capDesktop}

// capsCachePath 本地能力缓存路径。
func capsCachePath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}
	return filepath.Join(home, ".cubesandbox-sdk-go", "caps.json")
}

type capEntry struct {
	Caps      []string `json:"caps"`
	Source    string   `json:"source"` // probe | manual | heuristic
	CheckedAt string   `json:"checkedAt"`
}

func loadCapsCache() map[string]capEntry {
	m := map[string]capEntry{}
	b, err := os.ReadFile(capsCachePath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

func saveCapsCache(m map[string]capEntry) {
	p := capsCachePath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(p, b, 0o644)
	}
}

// heuristicCaps 依据镜像名推断能力（**保守**：只对 AIO 系镜像推断 browser/desktop，
// 其余镜像一律只给基线 shell/file/code；精确结果请用 tpl-caps --probe 落缓存）。
func heuristicCaps(imageInfo string) []string {
	img := strings.ToLower(imageInfo)
	caps := []string{capShell, capFile, capCode}
	switch {
	case strings.Contains(img, "aio-computer"):
		caps = append(caps, capBrowser, capDesktop) // 桌面镜像同时带 Chromium
	case strings.Contains(img, "aio-") || strings.Contains(img, "aiod") || strings.Contains(img, "all-in-one"):
		caps = append(caps, capBrowser)
	}
	return caps
}

// capsOf 取模板能力：缓存 → 启发式。第二个返回值是来源说明。
func capsOf(t tplView) ([]string, string) {
	cache := loadCapsCache()
	if e, ok := cache[t.TemplateID]; ok && len(e.Caps) > 0 {
		return e.Caps, e.Source
	}
	return heuristicCaps(t.ImageInfo), "heuristic"
}

// covers 判断 have 是否覆盖 need。
func covers(have, need []string) bool {
	set := map[string]bool{}
	for _, c := range have {
		set[strings.ToLower(c)] = true
	}
	for _, n := range need {
		if !set[strings.ToLower(strings.TrimSpace(n))] {
			return false
		}
	}
	return true
}

// parseNeeds 解析 --need=a,b 并校验取值。
func parseNeeds(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		ok := false
		for _, c := range allCaps {
			if p == c {
				ok = true
			}
		}
		if !ok {
			fatal("--need 不支持 %q（可选：%s）", p, strings.Join(allCaps, ","))
		}
		out = append(out, p)
	}
	return out
}

// rankLess 资源优先序：内存小 → CPU 小 → 多余能力少 → 创建新（未知规格排最后）。
// 「多余能力少」让 --need=browser 时优先选非桌面镜像（同等资源下更薄）。
func rankLess(a, b tplView) bool {
	am, bm := a.MemMB, b.MemMB
	if am == 0 {
		am = 1 << 30
	}
	if bm == 0 {
		bm = 1 << 30
	}
	if am != bm {
		return am < bm
	}
	ac, bc := a.CPU, b.CPU
	if ac == 0 {
		ac = 1 << 30
	}
	if bc == 0 {
		bc = 1 << 30
	}
	if ac != bc {
		return ac < bc
	}
	if len(a.caps) != len(b.caps) {
		return len(a.caps) < len(b.caps)
	}
	return a.CreatedAt > b.CreatedAt
}

// selectByNeed 选模板：READY + 能力覆盖 + 资源最小。
func selectByNeed(needs []string) (*tplView, error) {
	list, err := listTemplates()
	if err != nil {
		return nil, err
	}
	cands := []tplView{}
	for _, t := range list {
		if !strings.EqualFold(t.Status, "READY") {
			continue
		}
		caps, _ := capsOf(t)
		if covers(caps, needs) {
			v := t
			if d, err := templateDetail(t.TemplateID); err == nil {
				v = *d
			}
			v.caps = caps // 供排序用（已知能力数）
			cands = append(cands, v)
		}
	}
	if len(cands) == 0 {
		// 给出诊断：现有模板各有什么能力
		var sb strings.Builder
		for _, t := range list {
			caps, src := capsOf(t)
			fmt.Fprintf(&sb, "  %s %s（%s，来源 %s）\n", t.TemplateID, t.Status, strings.Join(caps, ","), src)
		}
		return nil, fmt.Errorf("没有满足 --need=%s 的 READY 模板；现有模板：\n%s（可用 tpl-caps --probe 精测能力）",
			strings.Join(needs, ","), sb.String())
	}
	sort.Slice(cands, func(i, j int) bool { return rankLess(cands[i], cands[j]) })
	return &cands[0], nil
}

// probeCaps 真机探测：建临时沙箱 → **直接打真实端点**判断能力 → 销毁。
//
//   browser：GET /v2/browser/screenshot == 200
//            桌面镜像的 Chromium 是 manual 模式：20s 还没起来就调 /opt/gem/browser-launch.sh（若存在）再等
//   desktop：GET /v2/computer/info == 200（worker 延迟启动，给足 60s 宽限；不能凭 503 报文提前否定）
//   探测预算：browser ≤ 150s、desktop ≤ 60s，两项都确定立刻返回。
func probeCaps(c *cubesandbox.Client, t tplView) ([]string, error) {
	d := 300 * time.Second
	opts := cubesandbox.CreateOptions{TemplateID: t.TemplateID, Timeout: &d}
	sb, err := c.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("探测用沙箱创建失败: %w", err)
	}
	defer func() {
		if err := sb.Kill(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[caps] 警告：探测沙箱 %s 销毁失败，请手动 rm：%v\n", sb.SandboxID, err)
		}
	}()
	co := cubesandbox.CommandOptions{Timeout: 60 * time.Second}

	// 先分清镜像类型：AIO 系镜像有 /opt/gem/run.sh（有网关/browser/desktop 面）；
	// 其余镜像（如官方 sandbox-code：envd + Jupyter）只做基线判定，不必干等网关。
	isAIO := false
	if res, err := sb.Commands().Run(ctx, "test -f /opt/gem/run.sh && echo yes", co); err == nil && strings.Contains(res.Stdout, "yes") {
		isAIO = true
	}
	if !isAIO {
		res, err := sb.Commands().Run(ctx, `curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:49983/health`, co)
		if err != nil || strings.TrimSpace(res.Stdout) != "204" {
			return nil, fmt.Errorf("沙箱 %s 既无 AIO 网关也无 envd，无法判定能力", sb.SandboxID)
		}
		fmt.Fprintf(os.Stderr, "[caps] %s 非 AIO 镜像（无 /opt/gem/run.sh）：记基线 shell,file,code\n", t.TemplateID)
		return []string{capShell, capFile, capCode}, nil
	}

	// AIO 镜像：等网关（镜像内 8080）
	gateway := false
	for i := 0; i < 90; i++ {
		res, err := sb.Commands().Run(ctx, `curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/v1/capabilities`, co)
		if err == nil && strings.TrimSpace(res.Stdout) == "200" {
			gateway = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !gateway {
		return nil, fmt.Errorf("沙箱 %s 为 AIO 镜像但网关 180s 内未就绪", sb.SandboxID)
	}

	// 打一次端点：返回 (http 码, 响应正文)
	probe := func(path string) (string, string) {
		res, err := sb.Commands().Run(ctx, fmt.Sprintf(`curl -s -w "\n%%{http_code}" http://127.0.0.1:8080%s`, path), co)
		if err != nil {
			return "", ""
		}
		out := strings.TrimSpace(res.Stdout)
		i := strings.LastIndex(out, "\n")
		if i < 0 {
			return out, ""
		}
		return strings.TrimSpace(out[i+1:]), out[:i]
	}

	browser, desktop, triedLaunch := false, false, false
	start := time.Now()
	deadline := start.Add(150 * time.Second)
	for {
		if !browser {
			if code, _ := probe("/v2/browser/screenshot"); code == "200" {
				browser = true
			} else if !triedLaunch && time.Since(start) > 20*time.Second {
				// 桌面镜像的 Chromium 需手动拉起（BROWSER_START_MODE=manual）
				triedLaunch = true
				if res, err := sb.Commands().Run(ctx, "test -x /opt/gem/browser-launch.sh && /opt/gem/browser-launch.sh >/dev/null 2>&1; echo done", co); err == nil && strings.Contains(res.Stdout, "done") {
					fmt.Fprintf(os.Stderr, "[caps] %s 触发 browser-launch.sh（manual 模式镜像）\n", t.TemplateID)
				}
			}
		}
		if !desktop {
			if code, _ := probe("/v2/computer/info"); code == "200" {
				desktop = true
			}
		}
		if (browser && desktop) || time.Now().After(deadline) {
			break
		}
		if !desktop && time.Since(start) > 60*time.Second {
			// worker 60s 没起：判定无桌面能力（之后循环只剩 browser 分支）
			desktop = false
			if browser {
				break
			}
		}
		time.Sleep(5 * time.Second)
	}

	caps := []string{capShell, capFile, capCode}
	if browser {
		caps = append(caps, capBrowser)
	}
	if desktop {
		caps = append(caps, capDesktop)
	}
	return caps, nil
}

// cmdTplCaps 展示（或探测）各模板能力。
//
//	tpl-caps [<模板ID>] [--probe] [--json]
func cmdTplCaps(c *cubesandbox.Client, args []string) {
	flags, tid, rest := splitArgs(args)
	foldFlags(flags, rest)
	list, err := listTemplates()
	if err != nil {
		fatal("列出模板失败: %v", err)
	}
	picked := []tplView{}
	for _, t := range list {
		if tid == "" || t.TemplateID == tid || t.matchAny([]string{tid}) {
			picked = append(picked, t)
		}
	}
	if len(picked) == 0 {
		fatal("没有匹配 %q 的模板", tid)
	}
	if flags["probe"] == "true" {
		cache := loadCapsCache()
		for _, t := range picked {
			fmt.Fprintf(os.Stderr, "[caps] 探测 %s …\n", t.TemplateID)
			caps, err := probeCaps(c, t)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[caps] %s 探测失败：%v\n", t.TemplateID, err)
				continue
			}
			cache[t.TemplateID] = capEntry{Caps: caps, Source: "probe", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
			fmt.Fprintf(os.Stderr, "[caps] %s → %s\n", t.TemplateID, strings.Join(caps, ","))
		}
		saveCapsCache(cache)
		fmt.Fprintf(os.Stderr, "[caps] 已写入 %s\n", capsCachePath())
	}
	type row struct {
		TemplateID string   `json:"templateID"`
		Aliases    []string `json:"aliases,omitempty"`
		Status     string   `json:"status"`
		Caps       []string `json:"caps"`
		Source     string   `json:"source"`
		CPU        int      `json:"cpuMilli,omitempty"`
		MemMB      int      `json:"memMiB,omitempty"`
	}
	rows := []row{}
	for _, t := range picked {
		caps, src := capsOf(t)
		d, err := templateDetail(t.TemplateID)
		if err != nil {
			d = &t
		}
		rows = append(rows, row{t.TemplateID, t.Aliases, t.Status, caps, src, d.CPU, d.MemMB})
	}
	if flags["json"] == "true" {
		printJSON(rows)
		return
	}
	fmt.Printf("%-34s %-20s %-7s %-12s %-9s %s\n", "模板ID", "别名", "状态", "CPU/内存", "能力来源", "能力")
	for _, r := range rows {
		alias := "-"
		if len(r.Aliases) > 0 {
			alias = strings.Join(r.Aliases, ",")
		}
		spec := "?"
		if r.CPU > 0 || r.MemMB > 0 {
			spec = fmt.Sprintf("%dm/%dMi", r.CPU, r.MemMB)
		}
		fmt.Printf("%-34s %-20s %-7s %-12s %-9s %s\n", r.TemplateID, alias, r.Status, spec, r.Source, strings.Join(r.Caps, ","))
	}
}
