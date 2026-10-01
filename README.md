# sandbox-tools

云沙箱的预编译 CLI 客户端。静态链接、无运行时依赖，下载即用。

## ctgo

CT 沙箱（`ct-sandbox-1.<private-host>`，agent-infra/sandbox v1.11.0 托管实例）的命令行客户端，
基于官方 Go SDK `github.com/agent-infra/sandbox-sdk-go`。

**为什么预编译**：客户端本身只是一个 HTTP 消费者，但解释器冷启动很贵 —— 在 aarch64 的 iSH 上，
Python SDK 每次调用要烧约 7 秒本机 CPU（`import httpx` 就占 3.1 秒），Go 静态二进制约 0.3 秒。
长任务差距更大：65 秒任务的异步轮询走 Go 客户端全程只占 0.37 秒 CPU。

### 安装

```sh
set -e
BASE="https://github.com/otaku-say/sandbox-tools/releases/latest/download"

case "$(uname -m)" in
  x86_64|amd64)  ASSET="ctgo-linux-amd64" ;;
  aarch64|arm64) ASSET="ctgo-linux-arm64" ;;
  *) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
curl -fsSL -o "$tmp/$ASSET"     "$BASE/$ASSET"
curl -fsSL -o "$tmp/SHA256SUMS" "$BASE/SHA256SUMS"
( cd "$tmp" && grep " $ASSET\$" SHA256SUMS | sha256sum -c - )

cp "$tmp/$ASSET" /usr/local/bin/ctgo && chmod +x /usr/local/bin/ctgo
rm -rf "$tmp"
ctgo version
```

> 安装目标必须是**保留可执行位的真实文件系统路径**（如 `/usr/local/bin`）。
> 某些挂载（如 Minis 的 `/var/minis`）不保留 exec 位，装在那里会 `Permission denied`。

### 配置

| 环境变量 | 必需 | 说明 |
|---|---|---|
| `CT_SANDBOX_1_KEY` | 是 | 沙箱 API key（`ctgo version` 可确认是否读到） |
| `CT_BASE` | 否 | 覆盖沙箱地址，默认 `https://ct-sandbox-1.<private-host>` |

### 用法

```
ctgo exec  "<cmd>"                同步执行（<60s）
ctgo run   "<cmd>" [hard秒]        长任务：async 派发 + 增量轮询
ctgo sess  <id> "<cmd>"           持久会话执行（cwd/env 跨调用保持）
ctgo sessnew <id> [dir]           建持久会话
ctgo sessions                     列出会话
ctgo view  <id>                   查看会话控制台
ctgo kill  <id>                   清理会话
ctgo job   <id> "<cmd>"           保活后台任务（nohup + 日志落盘）
ctgo log   <id> [行数]            读任务日志
ctgo read  <远端路径>              读远端文件到 stdout
ctgo write <远端路径> <本地文件>    本地 → 远端（二进制自动 base64）
ctgo get   <远端路径> <本地文件>    远端 → 本地
ctgo ps                           进程列表
ctgo health                       体检
ctgo version                      版本与连接配置
```

### 行为约定

- **60 秒网关限制**：`exec` 只用于 < 60 秒的命令；超时会得到「空输出 + 耗时 60~66 秒」的假成功。
  超过 60 秒一律用 `run`（异步派发 + 按 offset 增量轮询）。
- **保活**：`job` 把命令投进平台持久会话（底层即 tmux）并 nohup，日志写
  `/home/gem/jobs/<id>.log`，命令返回后任务继续运行。不要用裸 `tmux new-session`——进程组会随命令返回被回收。
- **网络健壮性**：内置 `TLSHandshakeTimeout: 30s`（模拟 CPU 上握手可能超 `net/http` 默认 10 秒）
  与 `WithMaxAttempts(3)` 重试。
- **已知上游缺陷**：Go SDK v0.0.5 时间戳解析要求带时区，而 REST 返回
  `2026-10-01T19:16:06.242026`，导致 `Shell.ListSessions` 直接报错。
  `ctgo sessions` 因此改用裸 HTTP 取 JSON。

## 从源码重建

iSH 本身没有 Go 工具链，可在任意有 Go 1.22+ 的 Linux（含沙箱自身）上交叉编译：

```sh
sh ctgo/build.sh          # 产出 dist/ctgo-linux-{amd64,arm64} 与 dist/SHA256SUMS
```

发布新版本时同步更新 `ctgo/main.go` 中的 `version` 常量。
