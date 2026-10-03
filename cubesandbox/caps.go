package main

// 模板画像：让调用方（Agent）按“需要什么能力”选模板，而不是记模板 ID。
//
//	简单编辑文件          → shell,file
//	Chrome 浏览器自动化    → shell,file,browser
//	远程桌面操控          → shell,file,browser,desktop
//
// ★ 设计原则：**能力与端口以“瞬间可得”的静态信息为主**，真机探测只作可选校正。
//
// 画像来源（优先级从高到低，前两级都是亚秒级、零沙箱）：
//   1) 本地缓存 ~/.cubesandbox-sdk-go/caps.json
//      —— 只有**模板指纹一致且未过期**（TTL 7 天）才算数；模板重建换 ID/时间 → 自动失效
//   2) 平台元数据静态推断：模板注解 com.exposed_ports（暴露端口）+ 镜像名
//      —— 端口本身就是最强的能力信号：带 9222(CDP) 的必是完整 AIO 镜像，
//         只暴露 18091/49983/49999 的轻量镜像没有浏览器
//   3) 镜像名启发式（**白名单**：aio-computer→+desktop；aio-daemon/aiod/all-in-one→+browser）
//   4) `--probe` 真机验证（建临时沙箱打端点；慢，仅在校正/存疑时用）
//
// 选择规则：在 READY 且**能力覆盖需求**的模板中，取**内存最小 → CPU 最小 → 创建最新**者。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// 探测结果有效期。模板指纹（镜像 + 创建时间）变了会立即失效，TTL 只是兜底。
const capsTTL = 7 * 24 * time.Hour

// ---------------- 端口目录（瞬间可得的静态信息） ----------------

// svcPort 一个服务端口及其语义。
type svcPort struct {
	Port int    `json:"port"`
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
}

// portCatalog 已知端口语义（来自本部署实测；未知端口标 unknown）。
var portCatalog = map[int]svcPort{
	49983: {49983, "envd", "执行/文件通道（所有镜像内置）"},
	18091: {18091, "aiod", "AIO 网关（轻量镜像 aio-code 用）"},
	8080:  {8080, "aio", "AIO 门户 + v2 API（完整镜像）"},
	8091:  {8091, "aio-alt", "AIO 备用端口"},
	9222:  {9222, "cdp", "Chrome DevTools（通常仅 loopback）"},
	5900:  {5900, "vnc", "VNC 桌面"},
	6080:  {6080, "novnc", "noVNC Web 桌面"},
	49999: {49999, "code-interpreter", "E2B 代码解释器"},
	8888:  {8888, "jupyter", "Jupyter Notebook"},
	8200:  {8200, "code-server", "VS Code Web"},
	18100: {18100, "aio-internal", "AIO 内部服务"},
}

// parsePortSpec 解析平台注解里的端口串（"8080:9222:49983" / "8080,9222"）。
func parsePortSpec(s string) []int {
	out := []int{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ',' || r == ' ' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// describePorts 把端口号翻译成带语义的列表。
func describePorts(ports []int) []svcPort {
	out := make([]svcPort, 0, len(ports))
	for _, p := range ports {
		if sp, ok := portCatalog[p]; ok {
			out = append(out, sp)
		} else {
			out = append(out, svcPort{p, "unknown", ""})
		}
	}
	return out
}

func hasPort(ports []int, p int) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// guessGateway 从暴露端口 + 镜像名判断 aiod 网关端口（0=无网关）。
//
// 实测依据：完整 AIO 镜像（aio-daemon / aio-computer）暴露 9222 且网关在 8080；
// 轻量镜像（aio-code）不暴露 9222，网关在 18091。
func guessGateway(ports []int, image string) int {
	if hasPort(ports, 9222) && hasPort(ports, 8080) {
		return 8080
	}
	if hasPort(ports, 8080) && !hasPort(ports, 18091) {
		return 8080
	}
	if hasPort(ports, 18091) {
		return 18091
	}
	if hasPort(ports, 8080) {
		return 8080
	}
	// 端口注解缺失时退回镜像名
	img := strings.ToLower(image)
	if strings.Contains(img, "aio-code") {
		return 18091
	}
	return 0
}

// capsFromStatic 依据暴露端口 + 镜像名推断能力（瞬间，零沙箱）。
func capsFromStatic(ports []int, image string) []string {
	img := strings.ToLower(image)
	hasBrowser := hasPort(ports, 9222) ||
		strings.Contains(img, "aio-daemon") || strings.Contains(img, "aio-computer") ||
		strings.Contains(img, "all-in-one") || strings.Contains(img, "aiod")
	hasDesktop := strings.Contains(img, "aio-computer") || strings.Contains(img, "desktop") ||
		hasPort(ports, 5900) || hasPort(ports, 6080)

	if !hasBrowser && !hasDesktop && !strings.Contains(img, "aio") && !strings.Contains(img, "aiod") && len(ports) == 0 {
		// 完全没有任何信号（无端口、无镜像名）→ 保守给基线
		return []string{capShell, capFile, capCode}
	}
	caps := []string{capShell, capFile, capCode}
	if hasBrowser {
		caps = append(caps, capBrowser)
	}
	if hasDesktop {
		caps = append(caps, capDesktop)
	}
	return caps
}

// ---------------- 缓存 ----------------

// capsCachePath 本地画像缓存路径。
func capsCachePath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}
	return filepath.Join(home, ".cubesandbox-sdk-go", "caps.json")
}

type capEntry struct {
	Caps      []string `json:"caps"`
	Source    string   `json:"source"` // probe | static | manual
	CheckedAt string   `json:"checkedAt"`
	// —— 模板指纹（写入时快照，用于判断缓存是否还对得上当前模板）——
	Image      string `json:"image,omitempty"`
	TemplateAt string `json:"templateCreatedAt,omitempty"`
	// —— 副产物 ——
	GatewayPort int       `json:"gatewayPort,omitempty"` // aiod 网关端口（0 = 无网关）
	Ports       []int     `json:"ports,omitempty"`       // 暴露端口（语义见 portCatalog）
	Spec        []svcPort `json:"portDetail,omitempty"`  // 带语义的端口（只读展示用）
	Note        string    `json:"note,omitempty"`
}

// valid 判断该缓存项对当前模板是否仍然可信。
func (e capEntry) valid(t tplView) bool {
	if len(e.Caps) == 0 {
		return false
	}
	if e.Image != "" && t.ImageInfo != "" && e.Image != t.ImageInfo {
		return false
	}
	if e.TemplateAt != "" && t.CreatedAt != "" && e.TemplateAt != t.CreatedAt {
		return false
	}
	ts, err := time.Parse(time.RFC3339, e.CheckedAt)
	if err != nil {
		return false
	}
	return time.Since(ts) <= capsTTL
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

// fillDetails 给模板补齐 CPU/内存/可写层/**暴露端口**（这些只有详情接口才有）。
// 详情接口不回 imageInfo，保留列表里的。
func fillDetails(t tplView) tplView {
	if d, err := templateDetail(t.TemplateID); err == nil {
		if d.ImageInfo == "" {
			d.ImageInfo = t.ImageInfo
		}
		return *d
	}
	return t
}

// ---------------- 画像 ----------------

// tplProfile 一个模板的完整画像：能力 + 端口 + 接入信息。
type tplProfile struct {
	TemplateID  string    `json:"templateID"`
	Caps        []string  `json:"caps"`
	Source      string    `json:"source"` // cache:probe | cache:static | static | heuristic
	GatewayPort int       `json:"gatewayPort,omitempty"`
	Ports       []svcPort `json:"ports,omitempty"`
	CheckedAt   string    `json:"checkedAt,omitempty"`
}

// refreshStaticCache 把静态推断结果写进缓存（下次 0 请求）。
// 已有 source=probe 的条目不会被静态结果覆盖。
func refreshStaticCache(profiles []tplProfile, list []tplView) {
	cache := loadCapsCache()
	dirty := false
	byID := map[string]tplView{}
	for _, t := range list {
		byID[t.TemplateID] = t
	}
	for _, p := range profiles {
		t, ok := byID[p.TemplateID]
		if !ok {
			continue
		}
		if e, ok := cache[p.TemplateID]; ok && e.valid(t) && e.Source == "probe" {
			continue // 真机结果更权威，别被静态覆盖
		}
		ports := []int{}
		for _, sp := range p.Ports {
			ports = append(ports, sp.Port)
		}
		cache[p.TemplateID] = capEntry{
			Caps: p.Caps, Source: "static", CheckedAt: time.Now().UTC().Format(time.RFC3339),
			Image: t.ImageInfo, TemplateAt: t.CreatedAt, GatewayPort: p.GatewayPort,
			Ports: ports, Spec: p.Ports,
		}
		dirty = true
	}
	if dirty {
		saveCapsCache(cache)
	}
}

// capsOf 兼容旧签名：返回 (能力, 来源, 网关端口)。
func capsOf(t tplView) ([]string, string, int) {
	p := profileOf(t)
	return p.Caps, p.Source, p.GatewayPort
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

// ---------------- 选择 ----------------

// selectByNeed 选模板：READY + 能力覆盖 + 资源最小。
// 画像来自缓存/平台元数据（瞬间），不需要建沙箱。
func selectByNeed(needs []string) (*tplView, error) {
	list, err := listTemplates()
	if err != nil {
		return nil, err
	}
	cands := []tplView{}
	fulls := []tplView{}
	profs := []tplProfile{}
	for _, t := range list {
		if !strings.EqualFold(t.Status, "READY") {
			continue
		}
		ft := fillDetails(t)
		p := profileOfFilled(ft)
		fulls = append(fulls, ft)
		profs = append(profs, p)
		if covers(p.Caps, needs) {
			ft.caps = p.Caps
			cands = append(cands, ft)
		}
	}
	refreshStaticCache(profs, fulls) // 顺手把静态画像写缓存，下次 0 请求
	if len(cands) == 0 {
		var sb strings.Builder
		for _, p := range profs {
			fmt.Fprintf(&sb, "  %s（%s，来源 %s）\n", p.TemplateID, strings.Join(p.Caps, ","), p.Source)
		}
		return nil, fmt.Errorf("没有满足 --need=%s 的 READY 模板；现有模板：\n%s（可用 tpl-caps --probe 精测能力）",
			strings.Join(needs, ","), sb.String())
	}
	sort.Slice(cands, func(i, j int) bool { return rankLess(cands[i], cands[j]) })
	return &cands[0], nil
}

// ---------------- 真机探测（可选校正） ----------------

// probeCaps 真机探测：建临时沙箱 → 画像 + 端点验证 → 销毁。
//
//	① envd(49983) 204 → 基线 shell,file,code 成立
//	② 静态画像：有无 aiod / Chromium / X11 —— 没有的就不去等端点（不白等超时）
//	③ aiod 网关端口（8080 惯例 / 18091 轻量镜像）
//	④ /v2/browser/screenshot == 200、/v2/computer/info == 200
//
// 返回 (能力, 网关端口, error)。
func probeCaps(c *cubesandbox.Client, t tplView) ([]string, int, error) {
	d := 300 * time.Second
	opts := cubesandbox.CreateOptions{TemplateID: t.TemplateID, Timeout: &d}
	sb, err := c.Create(ctx, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("探测用沙箱创建失败: %w", err)
	}
	defer func() {
		if err := sb.Kill(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[caps] 警告：探测沙箱 %s 销毁失败，请手动 rm：%v\n", sb.SandboxID, err)
		}
	}()
	co := cubesandbox.CommandOptions{Timeout: 30 * time.Second}
	exec := func(cmd string) string {
		res, err := sb.Commands().Run(ctx, cmd, co)
		if err != nil {
			return ""
		}
		return res.Stdout
	}

	// ① + ② 画像（重试到 envd 就绪，上限 60s）
	envdOK, hasAIOD, hasChrome, hasX11 := false, false, false, false
	profCmd := `echo "ENVD=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:49983/health)"
if command -v aiod >/dev/null 2>&1 || ls /usr/local/bin/aiod /opt/*/aiod >/dev/null 2>&1; then echo AIOD=1; else echo AIOD=0; fi
if command -v google-chrome >/dev/null 2>&1 || command -v chromium >/dev/null 2>&1 || command -v chromium-browser >/dev/null 2>&1; then echo CHROME=1; else echo CHROME=0; fi
if command -v Xvfb >/dev/null 2>&1 || command -v Xvnc >/dev/null 2>&1 || command -v x11vnc >/dev/null 2>&1 || command -v xfce4-session >/dev/null 2>&1 || command -v startxfce4 >/dev/null 2>&1; then echo X11=1; else echo X11=0; fi`
	pDeadline := time.Now().Add(60 * time.Second)
	for {
		out := exec(profCmd)
		envdOK = strings.Contains(out, "ENVD=204")
		hasAIOD = strings.Contains(out, "AIOD=1")
		hasChrome = strings.Contains(out, "CHROME=1")
		hasX11 = strings.Contains(out, "X11=1")
		if envdOK || time.Now().After(pDeadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !envdOK {
		return nil, 0, fmt.Errorf("沙箱 %s 的 envd(49983) 60s 内未就绪，无法判定能力", sb.SandboxID)
	}
	caps := []string{capShell, capFile, capCode}
	if !hasAIOD {
		return caps, 0, nil // 无 aiod 网关 → 没有 browser/desktop 面
	}

	// ③ 网关端口
	gwPort := 0
	gwDeadline := time.Now().Add(60 * time.Second)
	for {
		out := exec(`for p in 8080 18091; do c=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 http://127.0.0.1:$p/health); [ "$c" = "200" ] && { echo "GW=$p"; break; }; done`)
		if i := strings.Index(out, "GW="); i >= 0 {
			s := out[i+3:]
			if j := strings.IndexAny(s, "\n\r"); j >= 0 {
				s = s[:j]
			}
			if p, e := strconv.Atoi(strings.TrimSpace(s)); e == nil {
				gwPort = p
			}
		}
		if gwPort > 0 || time.Now().After(gwDeadline) {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if gwPort == 0 {
		fmt.Fprintf(os.Stderr, "[caps] %s 有 aiod 但网关 60s 未就绪，按基线记\n", t.TemplateID)
		return caps, 0, nil
	}

	// ④ 端点验证
	httpCode := func(path string) string {
		return strings.TrimSpace(exec(fmt.Sprintf(
			`curl -s -o /dev/null -w "%%{http_code}" --max-time 5 http://127.0.0.1:%d%s`, gwPort, path)))
	}
	if hasChrome {
		start := time.Now()
		launched := false
		for time.Since(start) < 90*time.Second {
			if httpCode("/v2/browser/screenshot") == "200" {
				caps = append(caps, capBrowser)
				break
			}
			if !launched && time.Since(start) > 20*time.Second {
				launched = true // 桌面镜像的 Chromium 是 manual 模式，需要主动拉起
				exec(`test -x /opt/gem/browser-launch.sh && /opt/gem/browser-launch.sh >/dev/null 2>&1; echo done`)
			}
			time.Sleep(5 * time.Second)
		}
	}
	if hasX11 {
		start := time.Now()
		for time.Since(start) < 45*time.Second {
			if httpCode("/v2/computer/info") == "200" {
				caps = append(caps, capDesktop)
				break
			}
			time.Sleep(5 * time.Second)
		}
	}
	return caps, gwPort, nil
}

// profileOfFilled 取画像（入参已补齐详情）。
func profileOfFilled(t tplView) tplProfile {
	cache := loadCapsCache()
	if e, ok := cache[t.TemplateID]; ok && e.valid(t) {
		spec := e.Spec
		if len(spec) == 0 && len(e.Ports) > 0 {
			spec = describePorts(e.Ports)
		}
		return tplProfile{
			TemplateID: t.TemplateID, Caps: e.Caps, Source: "cache:" + e.Source,
			GatewayPort: e.GatewayPort, Ports: spec, CheckedAt: e.CheckedAt,
		}
	}
	ports := parsePortSpec(t.Ports)
	caps := capsFromStatic(ports, t.ImageInfo)
	gw := guessGateway(ports, t.ImageInfo)
	src := "static"
	if len(ports) == 0 {
		src = "heuristic"
	}
	return tplProfile{
		TemplateID: t.TemplateID, Caps: caps, Source: src,
		GatewayPort: gw, Ports: describePorts(ports),
	}
}

// profileOf 惰性画像（未补齐详情的模板会补一次，亚秒级）。
func profileOf(t tplView) tplProfile {
	if !t.filled {
		t = fillDetails(t)
	}
	return profileOfFilled(t)
}

// printEndpointsHint 打印沙箱端点（瞬间：走模板画像，不额外建沙箱）。
// 让 Agent 建完沙箱立刻知道"有哪些端口、哪个是 AIO 网关、怎么拼 URL"。
func printEndpointsHint(sid, templateID string) {
	t, err := templateDetail(templateID)
	if err != nil {
		return
	}
	t.filled = true
	p := profileOfFilled(*t)
	if len(p.Caps) > 0 {
		fmt.Fprintf(os.Stderr, "[sandbox] 能力 %s（%s）\n", strings.Join(p.Caps, ","), p.Source)
	}
	if len(p.Ports) > 0 {
		parts := []string{}
		for _, sp := range p.Ports {
			parts = append(parts, fmt.Sprintf("%d %s", sp.Port, sp.Name))
		}
		fmt.Fprintf(os.Stderr, "[sandbox] 声明端口 %s\n", strings.Join(parts, " | "))
	}
	fmt.Fprintf(os.Stderr, "[sandbox] 提示：模板声明端口未必等于实际监听，用 `ports %s` 实测\n", sid)
	proxy := strings.TrimSuffix(envPick("", "CUBESANDBOX_PROXY_URL", "CBS_PROXY_BASE"), "/")
	if proxy == "" || p.GatewayPort <= 0 {
		return
	}
	base := fmt.Sprintf("%s/sandbox/%s", proxy, sid)
	fmt.Fprintf(os.Stderr, "[sandbox] AIO 网关 : %s/%d/   ← sandbox-sdk-go 的 SANDBOX_BASE\n", base, p.GatewayPort)
	fmt.Fprintf(os.Stderr, "[sandbox] envd     : %s/49983/\n", base)
}

// ---------------- 命令 ----------------

// cmdTplCaps 展示（或探测）各模板画像。
//
//	tpl-caps [<模板ID>] [--probe] [--prune] [--json]
func cmdTplCaps(c *cubesandbox.Client, args []string) {
	flags, tid, rest := splitArgs(args)
	foldFlags(flags, rest)
	list, err := listTemplates()
	if err != nil {
		fatal("列出模板失败: %v", err)
	}

	// --prune：清掉平台已不存在的模板条目（缓存长期堆积的旧 ID）
	if flags["prune"] == "true" {
		cache := loadCapsCache()
		live := map[string]bool{}
		for _, t := range list {
			live[t.TemplateID] = true
		}
		removed := 0
		for k := range cache {
			if !live[k] {
				delete(cache, k)
				removed++
			}
		}
		saveCapsCache(cache)
		fmt.Fprintf(os.Stderr, "[caps] 已清理 %d 个已消失模板的缓存条目（当前模板 %d 个）\n", removed, len(live))
	}

	picked := []tplView{}
	for _, t := range list {
		if tid == "" || t.TemplateID == tid || t.matchAny([]string{tid}) {
			picked = append(picked, fillDetails(t))
		}
	}
	if len(picked) == 0 {
		fatal("没有匹配 %q 的模板", tid)
	}

	if flags["probe"] == "true" {
		cache := loadCapsCache()
		picked = probeTargets(c, picked, cache)
	}

	rows := []tplProfile{}
	for _, t := range picked {
		p := profileOfFilled(t)
		rows = append(rows, p)
	}
	// 顺手刷新静态缓存（probe 结果不被覆盖）
	refreshStaticCache(rows, picked)

	if flags["json"] == "true" {
		printJSON(rows)
		return
	}
	fmt.Printf("%-34s %-9s %-6s %s\n", "模板ID", "来源", "网关", "能力 / 端口")
	for i, r := range rows {
		t := picked[i]
		spec := "?"
		if t.CPU > 0 || t.MemMB > 0 {
			spec = fmt.Sprintf("%dm/%dMi", t.CPU, t.MemMB)
		}
		gw := "-"
		if r.GatewayPort > 0 {
			gw = strconv.Itoa(r.GatewayPort)
		}
		ports := []string{}
		for _, sp := range r.Ports {
			ports = append(ports, strconv.Itoa(sp.Port))
		}
		fmt.Printf("%-34s %-9s %-6s %s\n", r.TemplateID, r.Source, gw, strings.Join(r.Caps, ","))
		fmt.Printf("%-34s %s %s\n", "", spec, strings.Join(ports, ","))
	}
}

// probeTargets 对给定模板做真机探测并写缓存。
func probeTargets(c *cubesandbox.Client, picked []tplView, cache map[string]capEntry) []tplView {
	for _, t := range picked {
		fmt.Fprintf(os.Stderr, "[caps] 探测 %s …\n", t.TemplateID)
		caps, gw, err := probeCaps(c, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[caps] %s 探测失败：%v\n", t.TemplateID, err)
			continue
		}
		ports := parsePortSpec(t.Ports)
		cache[t.TemplateID] = capEntry{
			Caps: caps, Source: "probe", CheckedAt: time.Now().UTC().Format(time.RFC3339),
			Image: t.ImageInfo, TemplateAt: t.CreatedAt, GatewayPort: gw,
			Ports: ports, Spec: describePorts(ports),
		}
		gwTxt := ""
		if gw > 0 {
			gwTxt = fmt.Sprintf("，网关 %d", gw)
		}
		fmt.Fprintf(os.Stderr, "[caps] %s → %s%s\n", t.TemplateID, strings.Join(caps, ","), gwTxt)
	}
	saveCapsCache(cache)
	fmt.Fprintf(os.Stderr, "[caps] 已写入 %s\n", capsCachePath())
	return picked
}
