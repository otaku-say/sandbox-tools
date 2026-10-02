# sandbox-tools

遥控 [CubeSandbox](https://github.com/TencentCloud/CubeSandbox) 云端沙箱的静态 CLI 工具集（面向 iSH / Alpine / 任意 Linux）。
两个工具都是 `CGO_ENABLED=0` 静态二进制，**下载即可运行**，无需 Go 工具链。

| 工具 | 用途 | 基于 |
|---|---|---|
| **`cubesandbox-sdk-go`** | 平台侧遥控：建/查/删沙箱、执行命令、文件传输 | 官方 SDK `github.com/tencentcloud/CubeSandbox/sdk/go` |
| **`sandbox-sdk-go`** | 沙箱内操作：命令、长任务、持久会话、文件读写 | `github.com/agent-infra/sandbox-sdk-go` |

## 下载

最新构建（滚动 Release，免登录）：

```
https://github.com/otaku-say/sandbox-tools/releases/tag/latest
```

资产：`cubesandbox-sdk-go-linux-{arm64,amd64}`、`sandbox-sdk-go-linux-{arm64,amd64}`、`SHA256SUMS`。

一键安装（iSH / Linux）：

```bash
BASE=https://github.com/otaku-say/sandbox-tools/releases/download/latest
curl -fsSL -o /tmp/csdk "$BASE/cubesandbox-sdk-go-linux-arm64"      # amd64 机器改后缀
curl -fsSL -o /tmp/sdk  "$BASE/sandbox-sdk-go-linux-arm64"
curl -fsSL -o /tmp/SUM  "$BASE/SHA256SUMS"
cd /tmp && grep -E 'cubesandbox-sdk-go-linux-arm64|sandbox-sdk-go-linux-arm64' SUM | sha256sum -c -
install -m 0755 /tmp/csdk /usr/local/bin/cubesandbox-sdk-go
install -m 0755 /tmp/sdk  /usr/local/bin/sandbox-sdk-go
```

## 用法

### cubesandbox-sdk-go（平台遥控）

官方 SDK 的数据面地址是 e2b 风格虚拟域名 `<port>-<sandboxID>.cube.app`；
在源站只放行 Cloudflare 的部署里直连不通，本工具在传输层把数据面改写成
CF 路径式路由 `https://<proxy>/sandbox/<sid>/<port>/<path>`，控制面原样透传 —— **不需要改服务端任何配置**。

```bash
export CUBE_API_KEY=<你的 CubeAPI 密钥>          # 控制面
SID=$(cubesandbox-sdk-go new --timeout 3600 --note 任务名)
cubesandbox-sdk-go ls ; cubesandbox-sdk-go info $SID
cubesandbox-sdk-go exec $SID "uname -a && id"
cubesandbox-sdk-go put $SID ./local.bin /tmp/local.bin
cubesandbox-sdk-go get $SID /tmp/out.txt ./out.txt
cubesandbox-sdk-go cat $SID /etc/hostname
cubesandbox-sdk-go rm $SID
cubesandbox-sdk-go health
```

环境变量：`CUBE_API_URL`（默认 `https://<cubesandbox-api-host>`）、`CUBE_API_KEY`、
`CUBE_TEMPLATE_ID`（默认内置模板）、`CBS_PROXY_BASE`（默认 `https://<cubesandbox-proxy-host>`）。

### sandbox-sdk-go（沙箱内操作）

```bash
export SANDBOX_BASE="https://<cubesandbox-proxy-host>/sandbox/<SID>/8080"
export SANDBOX_KEY=<可选，网关开启鉴权时填>
sandbox-sdk-go exec "python3 -V"
sandbox-sdk-go run "pip install pandas" 600      # 长任务（后台轮询）
sandbox-sdk-go job build "npm ci && npm run build" ; sandbox-sdk-go log build 100
sandbox-sdk-go write /tmp/a.txt ./a.txt ; sandbox-sdk-go get /tmp/out.tgz ./out.tgz
sandbox-sdk-go sessnew work /home/gem ; sandbox-sdk-go sess work "cd /etc && pwd"
```

## 自动构建

`.github/workflows/build.yml`：

- **push** 到 `main` → 立即构建并发布
- **每天 UTC 03:00** 检查上游依赖（CubeSandbox 平台 tag、agent-infra SDK 版本）；
  有更新才重建，结果写入 `upstream.json`
- **workflow_dispatch** 手动触发
- 矩阵：`{cubesandbox-sdk-go, sandbox-sdk-go} × {arm64, amd64}`，发布到滚动 Release `latest`（含 SHA256SUMS）

## 本地更新

```bash
# 检查是否有新的预编译二进制
curl -fsSL https://api.github.com/repos/otaku-say/sandbox-tools/releases/tags/latest \
  | grep -E '"published_at"|"name":'
```

源码目录：`cubesandbox-sdk-go/`、`sandbox-sdk-go/`（各自独立 `go.mod`）。
