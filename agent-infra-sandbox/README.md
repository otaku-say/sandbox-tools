# agent-infra-sandbox

封装上游 **[agent-infra/sandbox](https://github.com/agent-infra/sandbox)** 的 Go SDK
（`github.com/agent-infra/sandbox-sdk-go`）的命令行工具，产物名 **`sandbox-sdk-go`**。

面向"拿到沙箱之后"的全部操作：覆盖 SDK 的 **20 个命名空间 / 132 个方法**
（bash / shell / file / code / jupyter / nodejs / browser+6子域 / mcp / skills / sandbox / proxy / display / auth / util）。

## 文件

| 文件 | 内容 |
|---|---|
| `main.go` | 入口、客户端构造、基础命令（exec/run/job/sess/write/read/get/ps/health）、帮助 |
| `parse.go` | 统一的参数解析（`--key=value` 与位置参数） |
| `dispatch.go` | 命名空间 → 入口函数映射表（全部动作清单见文件头注释） |
| `cmd_file.go` | file 命名空间（17 个方法） |
| `cmd_code.go` | code / jupyter / nodejs / util |
| `cmd_browser.go` | browser + browserpage（35 个方法） |
| `cmd_browser_extra.go` | tabs / cookies / state / network / captcha |
| `cmd_mcp_skills.go` | mcp / skills |
| `cmd_ops.go` | sandbox(hooks/ctx) / proxy / display / auth |
| `API-REFERENCE.md` | 全量 API 盘点（含 Python/JS/Go 三方差集，编写实现的依据） |
| `build.sh` / `check-parity.sh` / `upstream.txt` | 构建 / 上游核对 / 版本记录 |

## 用法

```bash
export SANDBOX_BASE="https://<proxy>/sandbox/<SID>/8080"
export SANDBOX_KEY=<可选，网关开启鉴权时填>

# 基础
sandbox-sdk-go exec "python3 -V"
sandbox-sdk-go run "pip install pandas" 600
sandbox-sdk-go job build "npm ci && npm run build" ; sandbox-sdk-go log build 100

# 命名空间式（<ns> <action> [参数] [--选项=值]）
sandbox-sdk-go file read /etc/os-release
sandbox-sdk-go file grep /home/gem "TODO" --include=*.go --ci
sandbox-sdk-go page navigate https://example.com
sandbox-sdk-go page screenshot --out=shot.png
sandbox-sdk-go mcp tools
sandbox-sdk-go ctxinfo context
```

不带动作时打印该命名空间的用法：`sandbox-sdk-go page`

## 上游更新时

```bash
./check-parity.sh > /tmp/ai-api.txt     # 拉三语言最新方法清单
# 对照 API-REFERENCE.md 与 cmd_*.go 补缺口
./build.sh                              # 本地构建（GOARCH 可覆盖）
```

或：Actions → build-tools → Run workflow → `tool = agent-infra-sandbox`（只构建这一支）
