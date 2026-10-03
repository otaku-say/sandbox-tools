package main

// tpl-from-image：从 OCI 镜像的 registry 配置里读取「模板默认值」，直接生成或提交建模板请求。
//
// 读取顺序：
//   1) 镜像标签 io.cubesandbox.template.*（本仓库镜像在建镜像时写入，见 cubesandbox-image 仓库）
//      其中 io.cubesandbox.template.defaults 是 JSON 汇总，其余单键可覆盖汇总值
//   2) 无标签时回退到标准字段：Config.ExposedPorts（Dockerfile 的 EXPOSE）
// 镜像的 Config.Env 也会被打印（平台运行时会自动带上镜像 ENV，无需手填）。
//
// 用法：
//   tpl-from-image ghcr.io/otaku-say/cubesandbox-image/agent-infra/aio-daemon:latest            # 看默认值 + 请求体
//   tpl-from-image <ref> --json                                                                 # 只输出请求体 JSON
//   tpl-from-image <ref> --curl                                                                 # 输出可直接执行的 curl
//   tpl-from-image <ref> --create --alias=my-name --cpu=4000 --memory=4096                     # 直接提交到平台
//
// 依赖：只需 registry 的匿名读权限（公开镜像）；私有镜像传 --registry-user/--registry-pass

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ---------- registry 读取 ----------

type imgConfigBlob struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Env          []string            `json:"Env"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Labels       map[string]string   `json:"Labels"`
		Entrypoint   []string            `json:"Entrypoint"`
		Cmd          []string            `json:"Cmd"`
		Healthcheck  *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	} `json:"config"`
}

type imgManifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Platform  struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	} `json:"config"`
}

var acceptManifest = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// splitImageRef 拆分镜像引用：host / repo / tag|digest。
func splitImageRef(ref string) (host, repo, reference string) {
	ref = strings.TrimPrefix(strings.TrimPrefix(ref, "https://"), "http://")
	reference = "latest"
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		reference = ref[i+1:]
		ref = ref[:i]
	} else if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		reference = ref[i+1:]
		ref = ref[:i]
	}
	host = "registry-1.docker.io"
	if i := strings.Index(ref, "/"); i >= 0 && (strings.Contains(ref[:i], ".") || strings.Contains(ref[:i], ":") || ref[:i] == "localhost") {
		host = ref[:i]
		ref = ref[i+1:]
	}
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	repo = ref
	if host == "registry-1.docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return
}

// regGet 发一次 registry 请求；401 时用 WWW-Authenticate 换 token 重试。
func regGet(host, path, accept, user, pass string) ([]byte, error) {
	url := "https://" + host + path
	do := func(token string) (*http.Response, error) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if user != "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
		}
		return http.DefaultClient.Do(req)
	}
	resp, err := do("")
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", host, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		token, err := fetchToken(ch, user, pass)
		if err != nil {
			return nil, err
		}
		resp, err = do(token)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry 返回 HTTP %d: %s", resp.StatusCode, firstLine(body))
	}
	return body, nil
}

// fetchToken 按 WWW-Authenticate: Bearer realm=...,service=...,scope=... 换 token。
func fetchToken(challenge, user, pass string) (string, error) {
	if !strings.Contains(strings.ToLower(challenge), "bearer") {
		return "", fmt.Errorf("registry 要求认证但未给出 Bearer 挑战: %s", challenge)
	}
	kv := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(challenge, "Bearer "), ",") {
		p := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(p) == 2 {
			kv[p[0]] = strings.Trim(p[1], `"`)
		}
	}
	if kv["realm"] == "" {
		return "", fmt.Errorf("认证挑战缺少 realm: %s", challenge)
	}
	url := kv["realm"] + "?service=" + kv["service"]
	if kv["scope"] != "" {
		url += "&scope=" + kv["scope"]
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 registry token 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("解析 token 响应失败: %w", err)
	}
	if tok.Token == "" {
		tok.Token = tok.AccessToken
	}
	if tok.Token == "" {
		return "", fmt.Errorf("registry 未返回 token（镜像可能不存在或为私有）: %s", firstLine(body))
	}
	return tok.Token, nil
}

// fetchImageConfig 拉取镜像 config（自动处理 index → platform manifest）。
func fetchImageConfig(ref, platform, user, pass string) (*imgConfigBlob, error) {
	host, repo, reference := splitImageRef(ref)
	if platform == "" {
		platform = "linux/amd64"
	}
	manPath := "/v2/" + repo + "/manifests/" + reference
	body, err := regGet(host, manPath, acceptManifest, user, pass)
	if err != nil {
		return nil, err
	}
	var man imgManifest
	if err := json.Unmarshal(body, &man); err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}
	cfgDigest := man.Config.Digest
	if len(man.Manifests) > 0 { // 多架构索引：挑指定平台
		wantOS, wantArch := "linux", "amd64"
		if p := strings.SplitN(platform, "/", 2); len(p) == 2 {
			wantOS, wantArch = p[0], p[1]
		}
		cfgDigest = ""
		for _, m := range man.Manifests {
			if m.Platform.OS == wantOS && m.Platform.Architecture == wantArch {
				sub, err := regGet(host, "/v2/"+repo+"/manifests/"+m.Digest, acceptManifest, user, pass)
				if err != nil {
					return nil, err
				}
				var sm imgManifest
				if err := json.Unmarshal(sub, &sm); err != nil {
					return nil, fmt.Errorf("解析子 manifest 失败: %w", err)
				}
				cfgDigest = sm.Config.Digest
				break
			}
		}
		if cfgDigest == "" {
			return nil, fmt.Errorf("镜像 %s 没有 %s 架构", ref, platform)
		}
	}
	if cfgDigest == "" {
		return nil, fmt.Errorf("manifest 里没有 config（%s）", ref)
	}
	blob, err := regGet(host, "/v2/"+repo+"/blobs/"+cfgDigest, "", user, pass)
	if err != nil {
		return nil, err
	}
	var cfg imgConfigBlob
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return nil, fmt.Errorf("解析镜像 config 失败: %w", err)
	}
	return &cfg, nil
}

// ---------- 默认值提取 ----------

// tplDefaults 建模板请求体（字段名对齐 CubeSandbox WebUI/CubeAPI）。
type tplDefaults struct {
	Image             string   `json:"image"`
	WritableLayerSize string   `json:"writableLayerSize,omitempty"`
	ExposedPorts      []int    `json:"exposedPorts,omitempty"`
	ProbePort         int      `json:"probePort,omitempty"`
	ProbePath         string   `json:"probePath,omitempty"`
	CPU               int      `json:"cpu,omitempty"`
	Memory            int      `json:"memory,omitempty"`
	Env               []string `json:"env,omitempty"`
	Name              string   `json:"name,omitempty"`
}

func envKV(list []string) map[string]string {
	m := map[string]string{}
	for _, kv := range list {
		if i := strings.Index(kv, "="); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// defaultsFromImage 从镜像 config 合成模板默认值（标签优先，缺失用 EXPOSE 兜底）。
func defaultsFromImage(ref string, cfg *imgConfigBlob, flags map[string]string) (*tplDefaults, []string, error) {
	labels := cfg.Config.Labels
	d := &tplDefaults{Image: ref}
	warnings := []string{}

	// ① 标签里的 JSON 汇总
	if raw := labels["io.cubesandbox.template.defaults"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), d); err != nil {
			warnings = append(warnings, fmt.Sprintf("io.cubesandbox.template.defaults 不是合法 JSON，已忽略: %v", err))
		}
	}
	// ② 单键覆盖
	if v := labels["io.cubesandbox.template.exposed-ports"]; v != "" {
		d.ExposedPorts = parsePortList(v)
	}
	if v := labels["io.cubesandbox.template.probe-port"]; v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.ProbePort = n
		}
	}
	if v := labels["io.cubesandbox.template.probe-path"]; v != "" {
		d.ProbePath = strings.TrimSpace(v)
	}
	if v := labels["io.cubesandbox.template.writable-layer-size"]; v != "" {
		d.WritableLayerSize = strings.TrimSpace(v)
	}
	if v := labels["io.cubesandbox.template.cpu"]; v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.CPU = n
		}
	}
	if v := labels["io.cubesandbox.template.memory"]; v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.Memory = n
		}
	}
	if v := labels["io.cubesandbox.template.alias"]; v != "" {
		d.Name = strings.TrimSpace(v)
	}
	// ③ 无端口标签时用 EXPOSE 兜底
	if len(d.ExposedPorts) == 0 && len(cfg.Config.ExposedPorts) > 0 {
		seen := map[int]bool{}
		for p := range cfg.Config.ExposedPorts {
			n, err := strconv.Atoi(strings.TrimSuffix(p, "/tcp"))
			if err == nil && !seen[n] {
				seen[n] = true
				d.ExposedPorts = append(d.ExposedPorts, n)
			}
		}
		sort.Ints(d.ExposedPorts)
		warnings = append(warnings, "端口取自镜像 EXPOSE（无 io.cubesandbox.template 标签）")
	}
	// ④ 命令行覆盖
	if v := flags["alias"]; v != "" {
		d.Name = v
	}
	if v := flags["writable"]; v != "" {
		d.WritableLayerSize = v
	}
	if v := flags["cpu"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			d.CPU = n
		}
	}
	if v := flags["memory"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			d.Memory = n
		}
	}
	d.Env = envPairs(flags, "env")
	if d.WritableLayerSize == "" {
		warnings = append(warnings, "镜像未声明可写层大小，平台要求该字段：请用 --writable=12G 指定")
	}
	return d, warnings, nil
}

// envPairs 把 --env=K=V,... 转成 ["K=V"]。
func envPairs(flags map[string]string, key string) []string {
	v := flags[key]
	if v == "" {
		return nil
	}
	out := []string{}
	for _, kv := range strings.Split(v, ",") {
		if i := strings.Index(kv, "="); i > 0 {
			out = append(out, strings.TrimSpace(kv))
		}
	}
	return out
}

func parsePortList(s string) []int {
	out := []int{}
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// ---------- 命令入口 ----------

func cmdTplFromImage(args []string) {
	flags, pos, _ := splitArgs(args)
	need(len(pos) > 0, "用法: tpl-from-image <镜像引用> [--alias=] [--cpu=] [--memory=] [--writable=] [--env=K=V,...] [--json|--curl|--create]")
	ref := pos[0]
	user, pass := flags["registry-user"], flags["registry-pass"]

	cfg, err := fetchImageConfig(ref, flags["platform"], user, pass)
	if err != nil {
		fatal("读取镜像失败: %v", err)
	}
	d, warnings, err := defaultsFromImage(ref, cfg, flags)
	if err != nil {
		fatal("合成默认值失败: %v", err)
	}

	// --json：只输出请求体
	if flags["json"] == "true" {
		printJSON(d)
		return
	}
	// --curl：输出可直接执行的 curl
	if flags["curl"] == "true" {
		body, _ := json.Marshal(d)
		fmt.Printf("curl -sS -X POST \"$CUBESANDBOX_API_URL/templates\" \\\n  -H \"X-API-KEY: $CUBESANDBOX_API_KEY\" -H \"Content-Type: application/json\" \\\n  -d '%s'\n", string(body))
		return
	}
	// 人类可读摘要
	portStrs := make([]string, 0, len(d.ExposedPorts))
	for _, p := range d.ExposedPorts {
		portStrs = append(portStrs, strconv.Itoa(p))
	}
	fmt.Printf("镜像: %s\n", ref)
	fmt.Printf("  架构: %s/%s   入口: %s %s\n", cfg.OS, cfg.Architecture, strings.Join(cfg.Config.Entrypoint, " "), strings.Join(cfg.Config.Cmd, " "))
	fmt.Printf("  暴露端口: %s\n", strings.Join(portStrs, ","))
	fmt.Printf("  就绪探针: %d %s\n", d.ProbePort, d.ProbePath)
	fmt.Printf("  可写层: %s   CPU: %d  内存: %dMiB  别名: %s\n", d.WritableLayerSize, d.CPU, d.Memory, d.Name)
	if hc := cfg.Config.Healthcheck; hc != nil && len(hc.Test) > 0 {
		fmt.Printf("  HEALTHCHECK: %s\n", strings.Join(hc.Test, " "))
	}
	env := envKV(cfg.Config.Env)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("  镜像 ENV（%d 个，平台运行时会自动带上）: %s\n", len(keys), strings.Join(keys, ", "))
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "⚠️  %s\n", w)
	}
	fmt.Println()
	fmt.Println("建模板请求体（可直接粘进 WebUI 或 --json 管道使用）:")
	body, _ := json.MarshalIndent(d, "", "  ")
	fmt.Println(string(body))

	// --create：直接提交到平台
	if flags["create"] == "true" {
		raw, _ := json.Marshal(d)
		out, err := rawControlPost("/templates", raw)
		if err != nil {
			fatal("创建模板失败: %v", err)
		}
		fmt.Fprintf(os.Stderr, "✅ 已提交：%s\n", firstLine(out))
	}
}
