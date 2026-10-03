# sandbox-tools

遥控 [CubeSandbox](https://github.com/TencentCloud/CubeSandbox) 云端沙箱的静态 CLI 工具集（面向 iSH / Alpine / 任意 Linux）。
两个工具都是 `CGO_ENABLED=0` 静态二进制，**下载即可运行**，无需 Go 工具链。

| 工具 | 用途 | 基于 |
|---|---|---|
| **`cubesandbox-sdk-go`** | 平台侧遥控：建/查/删沙箱、执行命令、文件传输 | 官方 SDK `github.com/tencentcloud/CubeSandbox/sdk/go` |
| **`sandbox-sdk-go`** | 沙箱内操作：命令、文件、终端、监听、代码、浏览器、**computer-use**（77 条命令） | aiod **v2 HTTP API**（纯 v2，自实现客户端，不依赖上游 SDK） |

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

环境变量：`CUBESANDBOX_API_URL`（**必填**，控制面地址，例 `https://<cubesandbox-api-host>`）、`CUBESANDBOX_API_KEY`（部署密钥）、
`CUBESANDBOX_TEMPLATE_ID`（可选，默认模板）、`CUBESANDBOX_PROXY_URL`（**必填**，数据面网关，例 `https://<cubesandbox-proxy-host>`）。
（旧的 `CUBE_API_URL` / `CBS_PROXY_BASE` / `CUBE_API_KEY` / `CUBE_TEMPLATE_ID` 仍兼容，新名优先。）

### sandbox-sdk-go（沙箱内操作）

```bash
export SANDBOX_BASE="https://<cubesandbox-proxy-host>/sandbox/<SID>/8080"
export SANDBOX_KEY=<可选，网关开启鉴权时填>
sandbox-sdk-go exec "python3 -V"
ID=$(sandbox-sdk-go async "pip install pandas")   # 长任务：异步派发
sandbox-sdk-go log "$ID" --follow                # 跟踪到终态
sandbox-sdk-go put ./a.txt /tmp/a.txt ; sandbox-sdk-go get /tmp/out.tgz ./out.tgz
sandbox-sdk-go sess-new work --cwd=/home/gem ; sandbox-sdk-go sess work "pwd"
```

## 目录结构

```
cubesandbox/           封装上游 TencentCloud/CubeSandbox 的 Go SDK → 产物 cubesandbox-sdk-go
  main.go              入口 / 参数解析 / CF 路径改写传输层
  cmd_*.go             各能力域命令实现
  build.sh             本地构建（GOARCH 可覆盖）
  check-parity.sh      拉上游三语言 SDK 方法清单，核对覆盖度
  upstream.txt         对照的上游版本 + 差异结论 + 核对流程
agent-infra-sandbox/   aiod v2 API 遥控 CLI（纯 v2 HTTP，自实现）→ 产物 sandbox-sdk-go
  main.go              入口 / 命令表（77 条）
  client.go            v2 HTTP 核心（信封解析 / api / apiRaw / multipart）
  v2_*.go              各能力域实现（命令/文件/PTY/监听/代码/浏览器/桌面/MCP）
  tests/               集成套件（cli_it.py 77 用例 + run.sh 一键真机回归）
  build.sh             双架构构建 + SHA256SUMS
scripts/               与具体 SDK 无关的通用脚本（如 gosdk-update.py）
.github/workflows/     CI：构建 + 发布 + 上游版本跟踪
```

## 上游更新了怎么办

1. **看差异**：`cd cubesandbox && ./check-parity.sh`（或 agent-infra-sandbox 同名脚本）
   —— 拉取上游 Python / Node / JS / Go SDK 的最新方法清单，与本目录已实现的命令对照
2. **补实现**：按 `upstream.txt` 里记录的差异结论补 `cmd_*.go`
3. **只构建这一支**：
   - 本地：`./build.sh`（或 `GOARCH=amd64 ./build.sh`）
   - CI：Actions → **build-tools** → *Run workflow* → `tool` 选 `cubesandbox` 或 `agent-infra-sandbox`
     （单工具构建只重发该工具的资产，不动另一个）
4. 每天 UTC 03:00 的定时任务会自动检查上游版本；有变化才重建（记录写入 `upstream.json`）

## 本地更新

`scripts/gosdk-update.py`（安装到 `/usr/local/bin/gosdk-update`）：

```bash
gosdk-update check     # 看有没有新构建
gosdk-update update    # 下载 + SHA256 校验 + 安装
gosdk-update auto      # 有更新才装
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
