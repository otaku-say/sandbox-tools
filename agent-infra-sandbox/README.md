# sandbox-sdk-go（v5 · 纯 v2）

`sandbox-sdk-go` 是**拿到沙箱之后**的遥控 CLI：直接调用沙箱内 aiod 的 **v2 HTTP API**（纯标准库实现，不封装任何上游 SDK），把「命令 / 文件 / 终端 / 监听 / 代码 / 浏览器 / computer-use」共 **77 条命令**收进一个静态二进制 —— iSH / Alpine / 任意 Linux **下载即用**，零依赖。

- 本代（**v5**）为纯 v2 重写，与 v1（v4.x，封装 agent-infra Go SDK）**命令行不兼容**；迁移对照见下文《与 v1 的区别》。
- 分工：建/查/删沙箱、跨沙箱搬运 → 同仓库的 `cubesandbox-sdk-go`；沙箱内一切操作 → 本工具。
- 产物：`sandbox-sdk-go-linux-{arm64,amd64}` + `SHA256SUMS`，滚动发布在 [Release `latest`](https://github.com/otaku-say/sandbox-tools/releases/tag/latest)，本机可用 `gosdk-update` 自动升级。

## 安装

iSH / Linux（arm64 机器如下；amd64 把后缀换成 `amd64`）：

```bash
BASE=https://github.com/otaku-say/sandbox-tools/releases/download/latest
curl -fsSL -o /tmp/sdk "$BASE/sandbox-sdk-go-linux-arm64"
curl -fsSL -o /tmp/SUM "$BASE/SHA256SUMS"
cd /tmp && grep 'sandbox-sdk-go-linux-arm64' SUM | sha256sum -c -
install -m 0755 /tmp/sdk /usr/local/bin/sandbox-sdk-go
```

或者使用一键更新器（自动校验 SHA256 并安装到 `/usr/local/bin`）：

```bash
gosdk-update check     # 看有没有新构建
gosdk-update update    # 下载并安装（含校验）
gosdk-update auto      # 有更新才装（适合登录 / 定时调用）
```

## 快速开始

```bash
export SANDBOX_BASE="https://<cubesandbox-proxy-host>/sandbox/<沙箱ID>/8080"   # 必填
export SANDBOX_KEY=...               # 可选（网关开启鉴权时填）

sandbox-sdk-go version               # 版本 + SANDBOX_BASE 回显
sandbox-sdk-go health                # 平台健康检查
sandbox-sdk-go sandbox-info          # 沙箱环境自描述（工作区 / 语言 / 版本等）
sandbox-sdk-go exec "uname -a && python3 -V"
```

在沙箱内部自测时 `SANDBOX_BASE=http://127.0.0.1:8080` 即可（沙箱自带 aiod v2 API）。

## 环境变量与通用约定

| 变量 | 必填 | 说明 |
|---|---|---|
| `SANDBOX_BASE` | **是** | 沙箱的 aiod 基地址：`https://<网关>/sandbox/<沙箱ID>/8080`；沙箱内自测：`http://127.0.0.1:8080` |
| `SANDBOX_KEY` | 否 | 非空时随请求发送 `Authorization: Bearer` 与 `X-API-Key` |

- 取值参数一律 `--key=value`（**不支持** `--key value`）；布尔开关写 `--flag`。
- **每条命令都支持 `--json`**：置位时原样打印服务端 `data`（缩进 JSON），否则打印人类可读结果。
- 业务输出走 stdout；错误走 stderr 且退出码非 0，报错含「哪一步 + HTTP 状态码 + 服务端 message（hint）」。
- 服务端统一信封 `{success,message,data,hint}`；CLI 默认剥出 `data`。非 2xx 或 `success=false` 均视为失败。

## 命令表（77 条，与 `main.go` 的 `commands` 表一一对应）

### 基础 / 运维

| 命令 | 用法 |
|---|---|
| `version` | `version` |
| `health` | `health` |
| `sandbox-info` | `sandbox-info` |
| `sandbox-packages` | `sandbox-packages --lang=python\|node` |

### 命令（执行 / 长任务 / 会话）

| 命令 | 用法 |
|---|---|
| `exec` | `exec <命令> [--cwd=] [--env=K=V,K2=V2] [--timeout=] [--shell=] [--user=] [--max-output=] [--session=] \| exec --id=<id> [--offset=] [--stderr-offset=]` |
| `async` | `async <命令> [--cwd=] [--env=] [--user=]（mode=async，打印 command_id）` |
| `log` | `log <command_id> [--follow] [--interval=500ms] [--timeout=]` |
| `kill` | `kill <command_id> [--signal=SIGKILL]` |
| `stdin` | `stdin <command_id> <文本> [--enter]` |
| `sess-new` | `sess-new <session_id> [--cwd=] [--env=] [--user=]` |
| `sess` | `sess <session_id> <命令> [--timeout=] [--max-output=]` |
| `sess-ls` | `sess-ls` |
| `sess-rm` | `sess-rm <session_id>` |

### 文件（读写 / 检索 / 传输）

| 命令 | 用法 |
|---|---|
| `read` | `read <path> [--start=] [--end=] [--user=]` |
| `cat` | `cat <path> [--start=] [--end=]` |
| `write` | `write <本地文件\|-> <远端路径> [--append] [--user=]` |
| `ls` | `ls <path> [--recursive] [--hidden] [--depth=] [--user=]` |
| `stat` | `stat <path> [--user=]` |
| `tree` | `tree <path> [--depth=]` |
| `edit` | `edit <path> --old=<旧串> --new=<新串> [--replace-all] \| edit <path> --insert=<行号> --text=<内容>` |
| `grep` | `grep <path> <正则> [--fixed] [--ignore-case] [--include=*.go,*.py] [--max=]` |
| `search` | `search <path> <glob 如 **/*.py>` |
| `mkdir` | `mkdir <path> [--parents]` |
| `cp` | `cp <源> <目标> [--overwrite]` |
| `mv` | `mv <源> <目标> [--overwrite]` |
| `rm` | `rm <路径> [--recursive]` |
| `put` | `put <本地文件> <远端路径>（multipart 上传后移动到目标）` |
| `get` | `get <远端路径> <本地文件>（二进制安全下载）` |

### 终端（PTY，交互式）

| 命令 | 用法 |
|---|---|
| `pty-new` | `pty-new <会话id> [--cwd=] [--cols=] [--rows=] [--retention=persistent\|expiring]` |
| `pty` | `pty <会话id> <命令> [--timeout=] [--async]` |
| `pty-screen` | `pty-screen <会话id>` |
| `pty-input` | `pty-input <会话id> <文本> [--enter]` |
| `pty-signal` | `pty-signal <会话id> <信号，如 SIGINT>` |
| `pty-resize` | `pty-resize <会话id> --cols= --rows=` |
| `pty-ls` | `pty-ls` |
| `pty-rm` | `pty-rm <会话id>` |

### 监听（目录事件）

| 命令 | 用法 |
|---|---|
| `watch` | `watch <路径> [--recursive] [--debounce=毫秒]` |
| `watch-poll` | `watch-poll <watcher_id> [--cursor=] [--timeout=] [--limit=]` |
| `watch-ls` | `watch-ls` |
| `watch-rm` | `watch-rm <watcher_id>` |

### 代码（托管运行时）

| 命令 | 用法 |
|---|---|
| `code` | `code <源码> [--lang=python\|javascript] [--session=] [--timeout=]` |
| `code-info` | `code-info` |
| `code-sess-new` | `code-sess-new [--lang=python\|javascript]` |
| `code-sess-ls` | `code-sess-ls` |
| `code-sess-rm` | `code-sess-rm <session_id>` |

### 浏览器（Chromium）

| 命令 | 用法 |
|---|---|
| `br-info` | `br-info` |
| `br-go` | `br-go <url> [--wait=load\|domcontentloaded\|networkidle] [--timeout=]` |
| `br-shot` | `br-shot <输出文件.png> [--full] [--quality=0-100]` |
| `br-eval` | `br-eval <表达式> [--await]` |
| `br-snapshot` | `br-snapshot [--interactive]` |
| `br-click` | `br-click (--selector= \| --ref=)` |
| `br-fill` | `br-fill (--selector= \| --ref=) --value=<内容>` |
| `br-tabs` | `br-tabs` |
| `br-tab-new` | `br-tab-new [--url=]` |
| `br-tab-use` | `br-tab-use <tab_id>` |
| `br-tab-close` | `br-tab-close <tab_id>` |
| `br-cookies` | `br-cookies [--url=] [--domain=]` |
| `br-cookie-set` | `br-cookie-set --name= --value= [--url= \| --domain=]` |
| `br-network` | `br-network [--limit=] [--clear]` |
| `br-cdp` | `br-cdp <CDP 方法，如 Browser.getVersion> [--params=JSON]` |

### MCP

| 命令 | 用法 |
|---|---|
| `mcp` | `mcp <方法：initialize\|tools/list\|tools/call\|ping> [--params=JSON]` |

### computer-use（桌面自动化；需 aio-computer 镜像）

| 命令 | 用法 |
|---|---|
| `cmp-info` | `cmp-info（computer-use；需 aio-computer 镜像，aio-daemon 上返回 503）` |
| `cmp-shot` | `cmp-shot <输出文件.png>` |
| `cmp-cursor` | `cmp-cursor` |
| `cmp-clipboard` | `cmp-clipboard` |
| `cmp-windows` | `cmp-windows` |
| `cmp-a11y` | `cmp-a11y [--scope=] [--max-depth=] [--max-nodes=] [--role=] [--name=] [--match=] [--states=] [--include-offscreen=] [--timeout-ms=]` |
| `cmp-a11y-nodes` | `cmp-a11y-nodes [同 cmp-a11y] [--limit=] [--node-id=]` |
| `cmp-act` | `cmp-act '<JSON 动作>' [--screenshot]（例：'{"action":"click","x":100,"y":200}'）` |
| `cmp-act-batch` | `cmp-act-batch '<JSON 动作数组>' [--screenshot]` |
| `cmp-record` | `cmp-record [--action=start\|stop] [--fps=] [--crf=] [--max-duration=] [--width=] [--height=] [--save-path=]` |

### 终端 WebSocket / 事件流 / 补充

| 命令 | 用法 |
|---|---|
| `pty-ws` | `pty-ws <会话id> [--protocol=json\|binary] [--durable] [--restore] [--replay-bytes=]（WebSocket 附着终端，Ctrl-] 退出）` |
| `pty-ws-anon` | `pty-ws-anon [--protocol=json\|binary]（匿名 WebShell，断开即销毁）` |
| `watch-events` | `watch-events <watcher_id> [--max=]（SSE 事件流，Ctrl-C 退出）` |
| `br-upload` | `br-upload (--selector= \| --ref=) --paths=<沙箱内文件,...> [--tab-id=]（浏览器文件上传）` |
| `br-config` | `br-config [--resolution=1280x1024] \| [--json='{...}']（浏览器配置）` |
| `fs-tree-put` | `fs-tree-put <本地 tar 文件\|-> <远端目录> [--user=]（PUT /v2/fs/tree 整树上传）` |

## 典型场景

以下示例假定已 `export SANDBOX_BASE=...`。

### ① 巡检与环境

```bash
sandbox-sdk-go version && sandbox-sdk-go health
sandbox-sdk-go sandbox-info                              # 工作区 / python / node / 版本
sandbox-sdk-go sandbox-packages --lang=python | head     # 已装包清单（纯文本）
```

### ② 同步执行命令（默认 gem 用户）

```bash
sandbox-sdk-go exec "node -v; pwd"
sandbox-sdk-go exec "sudo -n whoami" --cwd=/home/gem --env=FOO=bar
```

### ③ 长任务：异步派发 + 跟踪（替代 v1 的 run / job）

```bash
ID=$(sandbox-sdk-go async "pip install pandas && python3 -c 'print(1+1)'")
sandbox-sdk-go log "$ID" --follow          # 跟到终态；也可 exec --id="$ID" 增量回读
sandbox-sdk-go kill "$ID"                  # 需要时终止（默认 SIGKILL）
```

> 单次 HTTP 受网关 ~100s 上限约束 —— 长任务一律 `async` + `log`，不要靠同步 `exec` 硬等。

### ④ 持久命令会话（cwd / env 固定在创建时）

```bash
sandbox-sdk-go sess-new build --cwd=/home/gem/proj --env=CI=1
sandbox-sdk-go sess build "make -j4"
sandbox-sdk-go sess-ls
```

### ⑤ 文件上传 / 下载 / 查看 / 编辑

```bash
sandbox-sdk-go put ./data.csv /tmp/data.csv && sandbox-sdk-go exec "wc -l /tmp/data.csv"
sandbox-sdk-go get /home/gem/result.zip ./result.zip
echo 'hello' | sandbox-sdk-go write - /tmp/hello.txt
sandbox-sdk-go read /tmp/app.py --start=0 --end=40
sandbox-sdk-go edit /tmp/app.py --old='debug=True' --new='debug=False'
sandbox-sdk-go grep /home/gem "TODO" --include=*.py --ignore-case
```

### ⑥ PTY 交互（tmux / vim / REPL）

```bash
sandbox-sdk-go pty-new t1 --cols=120 --rows=30
sandbox-sdk-go pty t1 "tmux new -As work"
sandbox-sdk-go pty-screen t1
sandbox-sdk-go pty-input t1 "ls" --enter
```

### ⑦ 监听目录变化

```bash
sandbox-sdk-go watch /tmp/out --recursive     # 记下输出的 watcher_id=…
sandbox-sdk-go watch-poll <watcher_id> --timeout=30
```

### ⑧ 代码执行（会话语义跨调用保持变量）

```bash
S=$(sandbox-sdk-go code-sess-new --lang=python | sed -n 's/^session_id: //p')
sandbox-sdk-go code "x = 41" --session="$S"
sandbox-sdk-go code "print(x + 1)" --session="$S"
```

### ⑨ 浏览器与 MCP

```bash
sandbox-sdk-go br-go "https://example.com" --wait=domcontentloaded
sandbox-sdk-go br-shot shot.png --full
sandbox-sdk-go br-snapshot --interactive
sandbox-sdk-go br-eval "document.title"
sandbox-sdk-go mcp tools/list
```

## v2 语义要点（实测）

- **超时**：`--timeout=N` 到点只返回 `status=running`（进程仍在跑），需要自己 `kill`。
- **kill**：终止后命令进入 `completed`、`exit_code=-1`（不是 -SIGKILL；勿用退出码反推信号）。
- **会话 cwd 固定**：命令会话的 cwd 在创建时确定；会话内 `cd` 不跨调用保留（每条命令仍是新进程）。
- **read 行号**：从 0 开始，`--end` 不含尾行。
- **packages 是文本**：`--lang=python|node`（`py` 会 400）。
- **put 是两步**：multipart 上传先落 `/tmp/<文件名>`，再移动到目标路径（CLI 已封装，同名覆盖）。
- **br-shot 是原始 PNG**：直接写盘并校验 PNG 魔术数；`--format=png|jpeg`（jpeg 才有 `--quality`）。
- **exec --env 实测生效**：`--env=K=V,K2=V2` 走请求体；`sess-new --env=` 同理。
- **pty-signal 语义偏重**：实测会终止会话进程（并非仅中断当前命令）。
- **computer-use**：`cmp-*` 需要 `aio-computer` 镜像（aio-daemon 镜像上返回 503，CLI 给出清晰报错）。

## 与 v1（v4.0.0）的区别

v1 = 封装 agent-infra/sandbox 官方 Go SDK（20 命名空间 / 132 方法）；v5 = 纯 HTTP 直调沙箱内 aiod v2 API（77 条命令）。命令名从「命名空间 + 动作」变为扁平命令，**命令行不兼容**，对照如下：

| v1 命令（v4.0.0） | v5 命令 | 说明 |
|---|---|---|
| `exec "<cmd>"` | `exec "<cmd>"` | 仍同步；新增 `--cwd/--env/--timeout/--shell/--user/--max-output/--session` |
| `run "<cmd>" [秒]` | `async "<cmd>"` + `log <id> --follow` | 长任务拆成「派发 + 跟踪」两步 |
| `job <id> "<cmd>"` / `log <id> [行数]` | `async` + `log <command_id>` | 不再 nohup、不再写本地 jobs 日志 |
| `sessnew <id> [dir]` | `sess-new <id> [--cwd=]` | 同语义 |
| `view` | `sess-ls` | 会话列表 |
| `kill`（会话 / 任务） | `kill <command_id>` / `sess-rm <session_id>` | 语义拆分 |
| `write <远端> <本地>` | `write <本地\|-> <远端>`（文本）/ `put <本地> <远端>`（二进制） | **参数顺序反转** |
| `read <远端>` | `read` / `cat <path>` | `--start/--end` 行号 0 起、end 不含尾行 |
| `ps` | 无（用 `exec "ps aux"` 代替） | v5 不提供进程视图 |
| `envpush` | 取消 | 改用 `exec --env=K=V,...` / `sess-new --env=` |
| `file list/read/write/str-replace/search/grep/upload/download` | `ls` / `read` / `write` / `edit` / `search` / `grep` / `put` / `get` | 命名空间并入顶层扁平命令 |
| `file watch-create/events/poll/stop` | `watch` / `watch-poll` / `watch-ls` / `watch-rm` | 另有 SSE 事件流：`watch-events` |
| `code run` / `jupyter` / `nodejs` | `code` / `code-sess-*`（`--lang=python\|javascript`） | 会话语义保留 |
| `browser` / `page *` | `br-info` / `br-go` / `br-shot` / `br-eval` / `br-snapshot` / `br-click` / `br-fill` | 其余动作可用 `br-eval` / `br-cdp` 兜底 |
| `tabs` / `cookies` / `net requests` | `br-tabs` 等 / `br-cookies` / `br-network` | |
| `mcp servers/tools/call` | `mcp initialize\|tools/list\|tools/call\|ping` | |
| `skills` / `hooks` / `proxy` / `display` / `auth` / `ctxinfo` | — | v2 API 无对应端点；沙箱上下文改用 `sandbox-info` |

更细的语义对照见 [COVERAGE.md](./COVERAGE.md)（76 个 v2 操作 → 命令映射）。

## 构建与发布

要求：Go 1.22+（纯标准库，无第三方依赖）。

```bash
./build.sh                        # linux/arm64 + linux/amd64 → ./dist/ + SHA256SUMS
VERSION=v5.0.1 ./build.sh         # 指定版本（-X main.Version=…）
GOARCHS="arm64" ./build.sh        # 只构建单架构
```

交叉编译固定 `CGO_ENABLED=0`、`-trimpath -ldflags "-s -w -X main.Version=<VERSION>"`，产物为静态且已 stripped 的 ELF，iSH / Alpine 直接可跑。

CI（`.github/workflows/sandbox-sdk-go.yml`）：push 到 main 或手动触发 → gofmt 检查、`go vet`、`go test`、双架构构建 + 冒烟，随后把 `sandbox-sdk-go-linux-{arm64,amd64}` 与 `SHA256SUMS` 发布到滚动 Release `latest`。手动触发可勾选跑集成测试（自建沙箱，需 `CUBE_API_KEY` secret）。

## 本机更新

```bash
gosdk-update check     # 只看有没有新版本
gosdk-update update    # 下载 + SHA256 校验 + 安装到 /usr/local/bin
gosdk-update auto      # 有更新才装
```

（更新器源码：仓库 `scripts/gosdk-update.py`。）

## 目录结构（本工具目录）

| 文件 | 内容 |
|---|---|
| `main.go` | 入口、命令表、用法输出与通用工具（冻结） |
| `client.go` | v2 纯 HTTP 客户端核心（信封解析 / api / apiRaw / multipart / `Version`） |
| `v2_commands.go` `v2_fs.go` `v2_pty.go` `v2_watch.go` `v2_code.go` `v2_browser*.go` `v2_mcp.go` `v2_computer.go` `v2_sandbox.go` `v2_stream.go` | 各能力域命令实现 |
| `build.sh` | 本地 / CI 构建脚本（双架构 + SHA256SUMS） |
| `tests/` | 集成测试（`cli_it.py` + 一键 `run.sh`，自建沙箱运行） |
| `V2-API.md` | aiod v2 API 路由 / 字段速查（从 OpenAPI 整理） |
| `COVERAGE.md` | 76 个 v2 操作 → 命令覆盖对照 |

## 许可

本目录代码为纯标准库实现，不含任何上游 SDK 代码。仓库（otaku-say/sandbox-tools）当前**未附带 LICENSE 文件**；在补充明确许可之前，默认保留所有权利（all rights reserved）。如需开源授权，建议由仓库所有者补充 LICENSE（如 MIT）。
