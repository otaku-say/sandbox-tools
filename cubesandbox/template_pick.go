package main

// 模板动态解析：不再依赖写死的模板 ID。
//
// 平台 API 提供的模板信息：
//   GET /templates           → templateID / status / createdAt / imageInfo / aliases[]
//   GET /templates/{id}      → 另含 replicas[0].spec（"cpu=2000m,mem=3072Mi"）与 createRequest 注解
//
// 选择优先级：
//   1) --template=<ID 或 别名/镜像子串>
//   2) 环境变量 CUBESANDBOX_TEMPLATE_ID
//   3) 动态选择（见 pickTemplate）：
//        ① CUBESANDBOX_TEMPLATE_PICK=子串1,子串2 …  按顺序匹配 别名/镜像（命中即选）
//        ② CUBESANDBOX_TEMPLATE_MIN_CPU=<毫核>、CUBESANDBOX_TEMPLATE_MIN_MEM=<MiB> 作为下限过滤
//        ③ 都没有：在 READY 里挑内存最小的（同则创建时间最新）

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// tplView 是模板的一行视图（列表 + 详情合并后的关键字段）。
type tplView struct {
	TemplateID string   `json:"templateID"`
	Aliases    []string `json:"aliases,omitempty"`
	Status     string   `json:"status,omitempty"`
	CreatedAt  string   `json:"createdAt,omitempty"`
	ImageInfo  string   `json:"imageInfo,omitempty"`
	CPU        int      `json:"cpuMilli,omitempty"`  // 毫核；0=未知
	MemMB      int      `json:"memMiB,omitempty"`    // MiB；0=未知
	WritableGB string   `json:"writableLayer,omitempty"`
	Ports      string   `json:"exposedPorts,omitempty"`

	caps []string `json:"-"` // 已知能力（选择器内部使用，不序列化）
}

// firstLine 取文本首行并截断（错误信息用）。
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// rawControl 直调控制面 API（读取 SDK 未暴露的字段）。
func rawControl(path string) ([]byte, error) {
	api := envPick(defAPIURL, "CUBESANDBOX_API_URL", "CUBE_API_URL")
	if api == "" {
		fatal("缺少 CUBESANDBOX_API_URL：请设置控制面地址（例 https://<cubesandbox-api-host>）")
	}
	req, err := http.NewRequest("GET", strings.TrimSuffix(api, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	if k := envPick("", "CUBESANDBOX_API_KEY", "CUBE_API_KEY"); k != "" {
		req.Header.Set("X-API-Key", k)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, firstLine(body))
	}
	return body, nil
}

// rawControlPost 直调控制面 POST。
func rawControlPost(path string, body []byte) ([]byte, error) {
	api := envPick(defAPIURL, "CUBESANDBOX_API_URL", "CUBE_API_URL")
	if api == "" {
		return nil, fmt.Errorf("缺少 CUBESANDBOX_API_URL")
	}
	req, err := http.NewRequest("POST", strings.TrimSuffix(api, "/")+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := envPick("", "CUBESANDBOX_API_KEY", "CUBE_API_KEY"); k != "" {
		req.Header.Set("X-API-Key", k)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, firstLine(out))
	}
	return out, nil
}

// listTemplates 拉模板列表（含别名 / 镜像 / 状态 / 创建时间）。
func listTemplates() ([]tplView, error) {
	body, err := rawControl("/templates")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		TemplateID string   `json:"templateID"`
		Status     string   `json:"status"`
		CreatedAt  string   `json:"createdAt"`
		ImageInfo  string   `json:"imageInfo"`
		Aliases    []string `json:"aliases"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析模板列表失败: %w", err)
	}
	out := make([]tplView, 0, len(raw))
	for _, t := range raw {
		out = append(out, tplView{
			TemplateID: t.TemplateID, Status: t.Status, CreatedAt: t.CreatedAt,
			ImageInfo: t.ImageInfo, Aliases: t.Aliases,
		})
	}
	// 新的在前（列表接口顺序不保证）
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// specFromText 解析 "cpu=2000m,mem=3072Mi" → (毫核, MiB)。
func specFromText(s string) (int, int) {
	cpu, mem := 0, 0
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		val := strings.TrimSpace(kv[1])
		switch strings.ToLower(kv[0]) {
		case "cpu":
			val = strings.TrimSuffix(val, "m")
			if n, err := strconv.Atoi(val); err == nil {
				cpu = n
			}
		case "mem", "memory":
			val = strings.TrimSuffix(strings.TrimSuffix(val, "Mi"), "MiB")
			if n, err := strconv.Atoi(val); err == nil {
				mem = n
			}
		}
	}
	return cpu, mem
}

// templateDetail 取单模板详情（补齐 CPU/内存/可写层/端口）。
func templateDetail(id string) (*tplView, error) {
	body, err := rawControl("/templates/" + id)
	if err != nil {
		return nil, err
	}
	var raw struct {
		TemplateID  string   `json:"templateID"`
		Status      string   `json:"status"`
		CreatedAt   string   `json:"createdAt"`
		ImageInfo   string   `json:"imageInfo"`
		Aliases     []string `json:"aliases"`
		Replicas    []struct {
			Spec string `json:"spec"`
		} `json:"replicas"`
		CreateRequest struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"createRequest"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析模板详情失败: %w", err)
	}
	v := &tplView{
		TemplateID: raw.TemplateID, Status: raw.Status, CreatedAt: raw.CreatedAt,
		ImageInfo: raw.ImageInfo, Aliases: raw.Aliases,
	}
	if len(raw.Replicas) > 0 {
		v.CPU, v.MemMB = specFromText(raw.Replicas[0].Spec)
	}
	v.WritableGB = raw.CreateRequest.Annotations["cube.master.rootfs.writable_layer_size"]
	v.Ports = raw.CreateRequest.Annotations["com.exposed_ports"]
	return v, nil
}

// matchAny 判断模板是否匹配任一子串（别名 / 镜像 / 模板 ID，忽略大小写）。
func (t tplView) matchAny(subs []string) bool {
	hay := strings.ToLower(t.TemplateID + " " + strings.Join(t.Aliases, " ") + " " + t.ImageInfo)
	for _, s := range subs {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && strings.Contains(hay, s) {
			return true
		}
	}
	return false
}

// display 一行可读描述。
func (t tplView) display() string {
	a := "-"
	if len(t.Aliases) > 0 {
		a = strings.Join(t.Aliases, ",")
	}
	spec := "?"
	if t.CPU > 0 || t.MemMB > 0 {
		spec = fmt.Sprintf("%dm/%dMiB", t.CPU, t.MemMB)
	}
	return fmt.Sprintf("%s [%s] %s %s", t.TemplateID, a, t.Status, spec)
}

// pickTemplate 动态选模板（见文件头规则）。
func pickTemplate() (*tplView, error) {
	list, err := listTemplates()
	if err != nil {
		return nil, err
	}
	ready := make([]tplView, 0, len(list))
	for _, t := range list {
		if strings.EqualFold(t.Status, "READY") {
			ready = append(ready, t)
		}
	}
	if len(ready) == 0 {
		return nil, fmt.Errorf("没有 READY 状态的模板（用 tpl-ls 查看）")
	}

	// ① 优先子串（别名/镜像）
	if pick := strings.TrimSpace(envPick("", "CUBESANDBOX_TEMPLATE_PICK")); pick != "" {
		subs := strings.Split(pick, ",")
		for _, sub := range subs { // 按用户给定顺序，先命中先选
			for _, t := range ready {
				if t.matchAny([]string{sub}) {
					return &t, nil
				}
			}
		}
	}

	// ② 规格下限过滤（需要详情里的 spec）
	minCPU, _ := strconv.Atoi(strings.TrimSpace(envPick("0", "CUBESANDBOX_TEMPLATE_MIN_CPU")))
	minMem, _ := strconv.Atoi(strings.TrimSpace(envPick("0", "CUBESANDBOX_TEMPLATE_MIN_MEM")))
	cands := ready
	if minCPU > 0 || minMem > 0 {
		filtered := make([]tplView, 0, len(ready))
		for _, t := range ready {
			d, err := templateDetail(t.TemplateID)
			if err != nil {
				continue
			}
			if (minCPU == 0 || d.CPU >= minCPU) && (minMem == 0 || d.MemMB >= minMem) {
				filtered = append(filtered, *d)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("没有满足规格下限的 READY 模板（MIN_CPU=%d MIN_MEM=%d）", minCPU, minMem)
		}
		cands = filtered
	}

	// ③ 缺省：内存最小者优先（同则创建时间最新）
	best := cands[0]
	for _, t := range cands[1:] {
		if best.MemMB == 0 || (t.MemMB > 0 && t.MemMB < best.MemMB) {
			best = t
		} else if t.MemMB == best.MemMB && t.CreatedAt > best.CreatedAt {
			best = t
		}
	}
	if d, err := templateDetail(best.TemplateID); err == nil {
		return d, nil
	}
	return &best, nil
}

// resolveTemplateID 统一入口：显式参数 → 环境变量 → 动态选择。
// 显式值与环境变量既可以是 tpl- 开头的 ID，也可以是别名/镜像子串。
func resolveTemplateID(explicit string) string {
	val := strings.TrimSpace(explicit)
	if val == "" {
		val = strings.TrimSpace(envPick("", "CUBESANDBOX_TEMPLATE_ID", "CUBE_TEMPLATE_ID"))
	}
	if val != "" {
		if strings.HasPrefix(val, "tpl-") {
			return val
		}
		// 别名/镜像子串 → 解析成真实 ID
		list, err := listTemplates()
		if err == nil {
			for _, t := range list {
				if t.matchAny([]string{val}) {
					fmt.Fprintf(os.Stderr, "[template] %q → %s\n", val, t.TemplateID)
					return t.TemplateID
				}
			}
		}
		fatal("找不到匹配 %q 的模板（用 tpl-ls 查看可用模板）", val)
	}
	t, err := pickTemplate()
	if err != nil {
		fatal("自动选择模板失败：%v（可用 --template= 或设置 CUBESANDBOX_TEMPLATE_ID）", err)
	}
	fmt.Fprintf(os.Stderr, "[template] 自动选择 %s\n", t.display())
	return t.TemplateID
}

// cmdTplLs 列出模板（含 CPU/内存/可写层/端口/镜像）。
func cmdTplLs2(args []string) {
	flags, _, _ := splitArgs(args)
	list, err := listTemplates()
	if err != nil {
		fatal("列出模板失败: %v", err)
	}
	views := make([]tplView, 0, len(list))
	for _, t := range list {
		if d, err := templateDetail(t.TemplateID); err == nil {
			views = append(views, *d)
		} else {
			views = append(views, t)
		}
	}
	if flags["json"] == "true" {
		printJSON(views)
		return
	}
	fmt.Printf("%-34s %-20s %-7s %-12s %-8s %s\n", "模板ID", "别名", "状态", "CPU/内存", "可写层", "镜像")
	for _, t := range views {
		alias := "-"
		if len(t.Aliases) > 0 {
			alias = strings.Join(t.Aliases, ",")
		}
		spec := "?"
		if t.CPU > 0 || t.MemMB > 0 {
			spec = fmt.Sprintf("%dm/%dMi", t.CPU, t.MemMB)
		}
		img := t.ImageInfo
		if i := strings.LastIndex(img, "/"); i >= 0 {
			img = img[i+1:]
		}
		if len(img) > 40 {
			img = img[:40] + "…"
		}
		fmt.Printf("%-34s %-20s %-7s %-12s %-8s %s\n", t.TemplateID, alias, t.Status, spec, t.WritableGB, img)
	}
}

// cmdTplPick 打印动态选择的模板（调试/脚本用）。
func cmdTplPick(args []string) {
	flags, _, _ := splitArgs(args)
	var t *tplView
	var err error
	if needs := flags["need"]; needs != "" {
		t, err = selectByNeed(parseNeeds(needs))
	} else {
		t, err = pickTemplate()
	}
	if err != nil {
		fatal("选择模板失败：%v", err)
	}
	if flags["json"] == "true" {
		printJSON(t)
		return
	}
	fmt.Println(t.TemplateID)
	fmt.Fprintf(os.Stderr, "%s\n", t.display())
}
