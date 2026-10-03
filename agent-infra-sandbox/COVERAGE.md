# sandbox-sdk-go v5 对 aiod v2 API 的覆盖率

数据来源：沙箱运行时 `/v2/openapi.json`（与上游仓库规范 **sha256 一致**：`811fb825e156a557a8e72fb2…`，aiod 0.9.2）。

**目标：76/76 = 100% 覆盖（含 computer-use，即使 aio-daemon 镜像自身不提供该 worker）。**

| 语义面 | 操作数 | CLI 命令 |
|---|---|---|
| 运维/信息 | 4 | `health` `sandbox-info` `sandbox-packages` `mcp` |
| 命令 | 7 | `exec` `async` `log` `kill` `stdin` `sess-new` `sess-ls` `sess-rm`（+`sess`） |
| 文件 | 15 | `read` `cat` `write` `ls` `stat` `tree` `edit` `grep` `search` `mkdir` `cp` `mv` `rm` `put` `get` + **`fs-tree-put`** |
| 终端 PTY | 11 | `pty-new` `pty` `pty-screen` `pty-input` `pty-signal` `pty-resize` `pty-ls` `pty-rm` + **`pty-ws`** **`pty-ws-anon`** |
| 监听 watch | 5 | `watch` `watch-poll` `watch-ls` `watch-rm` + **`watch-events`（SSE）** |
| 代码 | 6 | `code` `code-info` `code-sess-new` `code-sess-ls` `code-sess-rm` |
| 浏览器 | 18 | `br-info` `br-go` `br-shot` `br-eval` `br-snapshot` `br-click` `br-fill` `br-tabs` `br-tab-new` `br-tab-use` `br-tab-close` `br-cookies` `br-cookie-set` `br-network` `br-cdp` + **`br-upload`** **`br-config`** |
| 桌面 computer-use | 10 | **`cmp-info` `cmp-shot` `cmp-cursor` `cmp-clipboard` `cmp-windows` `cmp-a11y` `cmp-a11y-nodes` `cmp-act` `cmp-act-batch` `cmp-record`** |

## 说明

- **computer-use**：`aio-daemon` 镜像不含 computer-use worker（这些路由 503）；命令照样完整实现，
  换 `aio-computer` 镜像（XFCE + worker）即可用。CLI 在 503 时给出清晰报错与非 0 退出。
- **WebSocket**：`pty-ws` / `pty-ws-anon` 用标准库自实现 WS 客户端（握手 + 帧编解码），
  `protocol=json`（默认）/`binary`；`durable`/`restore`/`replay_bytes` 原样透传。
- **SSE**：`watch-events` 订阅 `/v2/watch/{id}/events`，逐事件打印；`--max=N` 收到 N 条退出
  （`watch-poll` 仍是长轮询的轻量替代）。
- **`PUT /v2/fs/tree`**：`fs-tree-put` 走原始 `application/x-tar` body（tar 直接 PUT 解包）。

## 沙箱自带文档入口（镜像内）

| 入口 | 说明 |
|---|---|
| `GET /v2/docs`、`GET /v1/docs` | HTML 文档（Scalar UI）；经网关即 `https://<proxy-host>/sandbox/<sid>/8080/v2/docs` |
| `GET /v2/openapi.json` | v2 机器可读规范（76 操作） |
| `GET /v1/openapi.json` | v1 兼容面规范 |
| `POST /mcp` | MCP hub；`tools/list` = 31 个工具（8 个 `sandbox_*` + 23 个 `browser_*`） |
| `/opt/aio/llms.txt` | 镜像自带机器可读环境说明 |

> `/metrics` 在本版 aiod（0.9.2）上实测 404（文档提过，运行时未实现），故不在覆盖范围。
