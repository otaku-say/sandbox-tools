# agent-infra sandbox — SDK 全量 API 参考（Go 主文档 + Python/JS 差集附录）

> 用途：为编写 Go CLI 封装提供精确方法签名与类型字段（避免编译错误），并给出 Go / Python / JS 三个 SDK 的命名空间与方法级差集。
>
> 盘点来源（只读源码，未构建、未创建沙箱）：
> - **Go SDK**：`github.com/agent-infra/sandbox-sdk-go`，分支 **`master`**（⚠️ 仓库**没有 `main` 分支**，默认分支即 `master`；另有 `skills`、`1.7.5` 分支，三者的 sandbox 客户端都不含 observe_* 方法），commit `105ef16155073b0cb2c3bd2008530d35e9e7b0d4`（2026-03-31）= tag **`v0.0.5`**（已核对：本地 CLI 项目 `go.mod` 固定的 `v0.0.5` 与该 commit 完全相同，故本文档对 v0.0.5 直接适用，无差异）。
> - **Python SDK**：`github.com/agent-infra/sandbox` → `sdk/python/agent_sandbox/`，commit `7f1afaf8d82bd30531a19caeb1a24dfebbc97d8c`（2026-09-14）。
> - **JS/TS SDK**：同一仓库 → `sdk/js/src/api/resources/`，同 commit `7f1afaf8`。
> - 方法提取自各子客户端目录的 `client.go`（`func (c *Client)` 声明）；字段提取自仓库根目录 `<namespace>.go` 的 struct 定义（含 json tag）。全部由脚本自动提取，未手工省略。

---

## 1. 顶层客户端一览

`client.NewClient()` 返回的 `*client.Client` 上挂载以下 20 个子客户端（字段名见 `client/client.go`）。

| 子客户端 | client.Client 字段 | Go 方法数 | Py/JS 方法数 | 一句话用途 |
|---|---|---|---|---|
| `sandbox` | `client.Sandbox` | 6 | 14（⚠️ 差集） | 沙箱环境信息：上下文 / 已装 Python·Node 包 / MCP Hook 管理 /（Py·JS 另有 observe_* 遥测） |
| `shell` | `client.Shell` | 12 | 12 | 交互式 shell 会话：执行命令、查看输出、写入 stdin、等待、杀进程、会话管理与统计、终端 URL |
| `bash` | `client.Bash` | 7 | 7 | bash 命令执行：同步/异步执行、增量输出轮询、写 stdin、发信号、会话管理 |
| `file` | `client.File` | 17 | 17 | 文件系统：读/写/替换/搜索、find/grep/glob、上传/下载、列目录、字符串替换编辑器、文件监视 watch |
| `jupyter` | `client.Jupyter` | 6 | 6 | Jupyter：创建会话、执行代码、内核信息、会话列举/删除 |
| `nodejs` | `client.Nodejs` | 7 | 7 | Node.js：创建会话、执行代码、运行时信息、会话 CRUD |
| `mcp` | `client.Mcp` | 3 | 3 | MCP：server 列表、工具枚举、调用工具 |
| `browser` | `client.Browser` | 6 | 6 | 浏览器实例级：信息、配置、重启、截图、代理 PAC、批量动作序列 execute_action |
| `browserpage` | `client.BrowserPage` | 29 | 29 | 页面级（方法最多）：导航/前进后退、点击/输入/按键/悬停、表单、滚动、截图、HTML/文本/Markdown、元素、控制台、录制、等待、JS 评估 |
| `browsertabs` | `client.BrowserTabs` | 4 | 4 | 标签页：列表、创建（新开页）、关闭、激活 |
| `browsercookies` | `client.BrowserCookies` | 3 | 3 | Cookie：读取、设置、清空 |
| `browserstate` | `client.BrowserState` | 2 | 2 | 浏览器状态（storage state）：保存 / 加载 |
| `browsernetwork` | `client.BrowserNetwork` | 6 | 6 | 网络：请求头（全局/按域名）、路由拦截增删、请求记录、HAR 导出 |
| `browsercaptcha` | `client.BrowserCaptcha` | 2 | 2 | 验证码：检测 / 等待识别 |
| `code` | `client.Code` | 2 | 2 | 代码执行：语言信息查询 + 执行代码 |
| `util` | `client.Util` | 1 | 1 | 工具：URI 转 Markdown |
| `skills` | `client.Skills` | 5 | 5 | 技能包：注册、元数据列表、读取内容、删除、清空 |
| `proxy` | `client.Proxy` | 11 | 11 | 代理：映射与排除项增删查、上游设置、健康检查、诊断 |
| `display` | `client.Display` | 1 | 1 | 显示录制（Xvfb）：Record |
| `auth` | `client.Auth` | 2 | 2 | 认证：创建 ticket、校验请求（供 nginx auth_request 场景） |

- ⚠️ **sandbox 是唯一有差集的命名空间**：Python/JS 各 14 个方法，Go 仅 6 个——Go 缺 8 个 `observe_*` 遥测方法（详见附录 B）。其余 19 个命名空间三方完全一致。
- 任务给出的清单里未提到 `Auth`，但 SDK 实际存在 `client.Auth`（2 个方法），本文按实际盘点。
- Go 字段是 `Nodejs`（不是 `NodeJs`）；Python 是 `nodejs`，JS 是 `nodejs`。
- 全 SDK 合计：**20 个子客户端、132 个公开方法**。（Python/JS 各 140 个。）

---

## 2. 编写 CLI 的通用约定（所有子客户端共用）

### 2.1 导入与构造

```go
import (
    sandboxsdkgo "github.com/agent-infra/sandbox-sdk-go"   // 根包，包声明名为 api，惯例别名为 sandboxsdkgo
    "github.com/agent-infra/sandbox-sdk-go/client"
    "github.com/agent-infra/sandbox-sdk-go/option"
    "github.com/agent-infra/sandbox-sdk-go/core"
)

c := client.NewClient(option.WithBaseURL("http://127.0.0.1:8080"))
// 子客户端字段：c.Sandbox / c.Shell / c.Bash / c.File / ... / c.Auth
resp, err := c.Bash.Exec(ctx, &sandboxsdkgo.BashExecRequest{Command: "ls"})
```

### 2.2 `NewClient` 与 option 选项

- `func NewClient(opts ...option.RequestOption) *Client`（`client` 包）
- 每个子客户端另有 `func NewClient(options *core.RequestOptions) *Client`（一般不用直接调，除非自己组装）
- 每个子客户端的 `Client` 结构体字段：`WithRawResponse *RawClient`（返回原始 HTTP 响应）、`options`/`baseURL`/`caller`（私有）

| option 函数 | 签名 | 作用 |
|---|---|---|
| `option.WithBaseURL` | `WithBaseURL(baseURL string) *core.BaseURLOption` | 覆盖服务地址（沙箱常见为 `http://<ip>:8080`） |
| `option.WithHTTPClient` | `WithHTTPClient(httpClient core.HTTPClient) *core.HTTPClientOption` | 自定义 HTTP 客户端（建议设置超时，否则默认无限等待） |
| `option.WithHTTPHeader` | `WithHTTPHeader(httpHeader http.Header) *core.HTTPHeaderOption` | 附加请求头（会 Clone） |
| `option.WithBodyProperties` | `WithBodyProperties(bodyProperties map[string]interface{}) *core.BodyPropertiesOption` | 合并进 JSON body 的额外字段 |
| `option.WithQueryParameters` | `WithQueryParameters(queryParameters url.Values) *core.QueryParametersOption` | 附加 query 参数 |
| `option.WithMaxAttempts` | `WithMaxAttempts(attempts uint) *core.MaxAttemptsOption` | 最大重试次数（默认 2，对 408/429/5xx 重试，尊重 Retry-After） |

以上选项既可以传给 `client.NewClient(...)`（全局生效），也可以作为**每个方法的最后一个参数** `opts ...option.RequestOption` 逐请求传入。

### 2.3 `core.RequestOptions` 字段（自定义组装时用）

- `BaseURL string`
- `HTTPClient HTTPClient`（接口：`Do(*http.Request) (*http.Response, error)`）
- `HTTPHeader http.Header`
- `BodyProperties map[string]interface{}`
- `QueryParameters url.Values`
- `MaxAttempts uint`

### 2.4 方法形态与返回

- 所有公开方法形如：`func (c *Client) Xxx(ctx context.Context, request *sandboxsdkgo.XxxRequest, opts ...option.RequestOption) (*sandboxsdkgo.ResponseXxx, error)`（部分方法无 request；有路径参数时如 `sessionId string`、`watcherId string`）。
- 普通方法返回 `(*ResponseXxx, error)`；`ResponseXxx` 包装了 `Success/Message/Data/Hint` 四个字段（见 §3.0 共享类型）。
- 少数方法返回特殊类型：`c.File.DownloadFile`、`c.BrowserPage.Screenshot` 返回 `io.Reader`（流式下载）；`c.File.Watch*`、`c.Auth.*` 返回 `any` / `map[string]any` / `map[string]string`；`c.Browser.GetProxyPac` 仅返回 `error`。
- 需要响应头/状态码时用 Raw 通道：`c.<Sub>.WithRawResponse.<Method>(...)`，返回 `*core.Response[T]`（字段 `StatusCode int`、`Header http.Header`、`Body T`）。

### 2.5 错误处理

```go
resp, err := c.Sandbox.RegisterHook(ctx, req)
if err != nil {
    var apiErr *core.APIError
    if errors.As(err, &apiErr) { /* apiErr.StatusCode 等 */ }
    var vErr *sandboxsdkgo.UnprocessableEntityError // 422 校验错误
    if errors.As(err, &vErr) { _ = vErr.Body /* *HttpValidationError */ }
}
```
- 所有非 2xx 响应转成 `*core.APIError`（支持 `errors.Is/As`）；422 映射为 `*sandboxsdkgo.UnprocessableEntityError`（内嵌 `*core.APIError` + `Body *HttpValidationError`）。

### 2.6 字段 Setter / Getter 与显式 null

- 每个 struct 字段都有 `GetXxx()`（nil 安全）与 `SetXxx(v)` 方法；`SetXxx` 会把字段标记为「显式设置」，序列化时即使为零值也不会被省略（用于显式发 `null`）。
- `GetExtraProperties()` 返回响应里未建模的额外 JSON 字段；`String()` 便于调试打印。
- 枚举类型（如 `Button`、`Language`、`Command` 等）提供 `NewXxxFromString(s string) (Xxx, error)` 与 `(x Xxx) Ptr() *Xxx`。

---

## 3. Go SDK 各子客户端 API（全量）

> 本章方法行格式：`func (c *Client) 方法名(参数) -> 返回`（即 `方法名(参数) -> 返回` 的完整形态；把 `->` 换成空格即为可编译的 Go 签名）。
> 每个方法最后一个参数都是 `opts ...option.RequestOption`。所有类型都在根包（import 别名 `sandboxsdkgo`）。

### 3.0 共享类型（`types.go` / `errors.go`，多个子客户端引用）

以下类型被多个子客户端的请求/响应包装直接引用；在各子客户端章节中只标注「共享类型，见 §3.0」，不重复展开。

##### `Response` — 通用响应包装
定义于 `types.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `interface{}` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseDict` — 通用响应包装
定义于 `types.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `map[string]interface{}` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseStr` — 通用响应包装
定义于 `types.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*string` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseList` — 通用响应包装
定义于 `types.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `[]interface{}` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseListStr` — 通用响应包装
定义于 `types.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `[]string` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `HttpValidationError` — 422 校验错误体
定义于 `types.go`。
- `Detail` `[]*ValidationError` json:`detail,omitempty`

##### `ValidationError` — 校验错误明细
定义于 `types.go`。
- `Loc` `[]*ValidationErrorLocItem` json:`loc`
- `Msg` `string` json:`msg`
- `Type` `string` json:`type`

##### `ValidationErrorLocItem` — 校验错误位置项（union）
定义于 `types.go`。
- `String` `string`
- `Integer` `int`

##### `UnprocessableEntityError` — 422 错误类型（errors.As 用）
> Validation Error
定义于 `errors.go`。
- `Body` `*HttpValidationError`
- （另内嵌 `*core.APIError`，经它提供 `StatusCode` / `Header` / `Body` 等；`Unwrap()` 返回该 APIError）

### 3.1 `sandbox` — `client.Sandbox`

> 沙箱环境信息：上下文 / 已装 Python·Node 包 / MCP Hook 管理 /（Py·JS 另有 observe_* 遥测）

- 构造：`sandbox.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Sandbox.WithRawResponse`。
- 方法数：6；涉及类型：13（直接 6 + 嵌套 7）。

**方法（6）**

- `func (c *Client) GetContext(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.SandboxResponse, error)` — Get sandbox environment information
- `func (c *Client) GetPythonPackages(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Get installed Python packages
- `func (c *Client) GetNodejsPackages(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Get installed Node.js packages
- `func (c *Client) ListHooks(ctx context.Context, request *sandboxsdkgo.SandboxListHooksRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListSandboxHook, error)` — List registered lifecycle hooks, optionally filtered by event.
- `func (c *Client) RegisterHook(ctx context.Context, request *sandboxsdkgo.RegisterHookRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseSandboxHook, error)` — Register a lifecycle hook.
- `func (c *Client) RemoveHook(ctx context.Context, name string, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Remove a hook by name.

**类型**

##### `RegisterHookRequest` — 请求（RegisterHook）
定义于 `sandbox.go`。
- `Name` `string` json:`name` — Unique name for this hook
- `Event` `*string` json:`event,omitempty` — Lifecycle event: "shutdown"
- `Command` `string` json:`command` — Shell command to execute
- `Timeout` `*float64` json:`timeout,omitempty` — Per-hook timeout in seconds
- `Priority` `*int` json:`priority,omitempty` — Execution priority (lower = earlier).

##### `SandboxListHooksRequest` — 请求（ListHooks）
定义于 `sandbox.go`。
- `Event` `*string` json:`-`

- `Response` — 响应（GetPythonPackages, GetNodejsPackages, RemoveHook）：共享类型（types.go），字段见 §3.0。

##### `ResponseListSandboxHook` — 响应（ListHooks）
定义于 `sandbox.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `[]*SandboxHook` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseSandboxHook` — 响应（RegisterHook）
定义于 `sandbox.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*SandboxHook` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `SandboxResponse` — 响应（GetContext）
定义于 `sandbox.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `interface{}` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)
- `HomeDir` `string` json:`home_dir`
- `Workspace` `*string` json:`workspace,omitempty`
- `Version` `string` json:`version`
- `Detail` `*SandboxDetail` json:`detail`

**嵌套类型**（响应/请求内引用）

##### `SandboxHook` — 嵌套（被 `ResponseListSandboxHook.Data` 引用）
定义于 `sandbox.go`。
- `Name` `string` json:`name` — Unique name for this hook
- `Event` `*string` json:`event,omitempty` — Lifecycle event: "shutdown"
- `Command` `string` json:`command` — Shell command to execute
- `Timeout` `*float64` json:`timeout,omitempty` — Per-hook timeout in seconds
- `Priority` `*int` json:`priority,omitempty` — Execution priority (lower = earlier).
- `Source` `*string` json:`source,omitempty` — Registration source: "env" or "api"

##### `SandboxDetail` — 嵌套（被 `SandboxResponse.Detail` 引用）
定义于 `sandbox.go`。
- `System` `*SystemEnv` json:`system`
- `Runtime` `*RuntimeEnv` json:`runtime`
- `Utils` `[]*ToolCategory` json:`utils`

##### `RuntimeEnv` — 嵌套（被 `SandboxDetail.Runtime` 引用）
定义于 `sandbox.go`。
- `Python` `[]*ToolSpec` json:`python`
- `Nodejs` `[]*ToolSpec` json:`nodejs`

##### `SystemEnv` — 嵌套（被 `SandboxDetail.System` 引用）
定义于 `sandbox.go`。
- `Os` `string` json:`os`
- `OsVersion` `string` json:`os_version`
- `Arch` `string` json:`arch`
- `User` `string` json:`user`
- `HomeDir` `string` json:`home_dir`
- `Workspace` `*string` json:`workspace,omitempty`
- `Timezone` `string` json:`timezone`
- `OccupiedPorts` `[]string` json:`occupied_ports`

##### `ToolCategory` — 嵌套（被 `SandboxDetail.Utils` 引用）
定义于 `sandbox.go`。
- `Category` `string` json:`category` — Name of tool category
- `Tools` `[]*AvailableTool` json:`tools` — List of tools under this category

##### `ToolSpec` — 嵌套（被 `RuntimeEnv.Python` 引用）
定义于 `sandbox.go`。
- `Ver` `*string` json:`ver,omitempty`
- `Bin` `*string` json:`bin,omitempty`
- `Alias` `[]string` json:`alias,omitempty`

##### `AvailableTool` — 嵌套（被 `ToolCategory.Tools` 引用）
定义于 `sandbox.go`。
- `Name` `string` json:`name` — Tool’s command / binary name
- `Description` `*string` json:`description,omitempty` — Tool’s functionality description


---

### 3.2 `shell` — `client.Shell`

> 交互式 shell 会话：执行命令、查看输出、写入 stdin、等待、杀进程、会话管理与统计、终端 URL

- 构造：`shell.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Shell.WithRawResponse`。
- 方法数：12；涉及类型：28（直接 17 + 嵌套 11）。

**方法（12）**

- `func (c *Client) ExecCommand(ctx context.Context, request *sandboxsdkgo.ShellExecRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellCommandResult, error)` — Execute command in the specified shell session Supports SSE streaming if Accept header contains 'text/event-stream'
- `func (c *Client) View(ctx context.Context, request *sandboxsdkgo.ShellViewRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellViewResult, error)` — View output of the specified shell session Supports SSE streaming if Accept header contains 'text/event-stream'
- `func (c *Client) WaitForProcess(ctx context.Context, request *sandboxsdkgo.ShellWaitRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellWaitResult, error)` — Wait for the process in the specified shell session to return
- `func (c *Client) WriteToProcess(ctx context.Context, request *sandboxsdkgo.ShellWriteToProcessRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellWriteResult, error)` — Write input to the process in the specified shell session
- `func (c *Client) KillProcess(ctx context.Context, request *sandboxsdkgo.ShellKillProcessRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellKillResult, error)` — Terminate the process in the specified shell session
- `func (c *Client) CreateSession(ctx context.Context, request *sandboxsdkgo.ShellCreateSessionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellCreateSessionResponse, error)` — Create a new shell session and return its ID If id already exists, return the existing session
- `func (c *Client) UpdateSession(ctx context.Context, request *sandboxsdkgo.ShellUpdateSessionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Update shell session configuration (e.g., no_change_timeout)
- `func (c *Client) GetTerminalUrl(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseStr, error)` — Create a new shell session and return the terminal URL
- `func (c *Client) GetSessionStats(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseShellSessionStats, error)` — Return aggregate statistics for shell sessions.
- `func (c *Client) ListSessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseActiveShellSessionsResult, error)` — List all active shell sessions
- `func (c *Client) CleanupAllSessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Cleanup all active shell sessions
- `func (c *Client) CleanupSession(ctx context.Context, sessionId string, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Manually cleanup a specific shell session

**类型**

##### `ShellCreateSessionRequest` — 请求（CreateSession）
定义于 `shell.go`。
- `Id` `*string` json:`id,omitempty` — Unique identifier for the shell session, auto-generated if not provided
- `ExecDir` `*string` json:`exec_dir,omitempty` — Working directory for the new session (must use absolute path)
- `NoChangeTimeout` `*int` json:`no_change_timeout,omitempty` — Timeout (seconds) for detecting no new output from commands in this session.
- `PreserveSymlinks` `*bool` json:`preserve_symlinks,omitempty` — If True, preserve symlinks in working directory path (pwd shows symlink path).

##### `ShellExecRequest` — 请求（ExecCommand）
定义于 `shell.go`。
- `Id` `*string` json:`id,omitempty` — Unique identifier of the target shell session, if not provided, one will be automatically created
- `ExecDir` `*string` json:`exec_dir,omitempty` — Working directory for command execution (must use absolute path)
- `Command` `string` json:`command` — Shell command to execute
- `AsyncMode` `*bool` json:`async_mode,omitempty` — Whether to execute command asynchronously (default: False for async, False for synchronous execution)
- `Timeout` `*float64` json:`timeout,omitempty` — Maximum time (seconds) to wait for command completion before returning running status
- `Strict` `*bool` json:`strict,omitempty` — Strict mode for working directory validation.
- `NoChangeTimeout` `*int` json:`no_change_timeout,omitempty` — Timeout (seconds) for detecting no new output from a command.
- `HardTimeout` `*float64` json:`hard_timeout,omitempty` — Hard timeout (seconds) for command execution.
- `PreserveSymlinks` `*bool` json:`preserve_symlinks,omitempty` — If True, preserve symlinks in working directory path (pwd shows symlink path).
- `Truncate` `*bool` json:`truncate,omitempty` — If True, truncate output when it exceeds 30000 characters (default: True)

##### `ShellKillProcessRequest` — 请求（KillProcess）
定义于 `shell.go`。
- `Id` `string` json:`id` — Unique identifier of the target shell session

##### `ShellUpdateSessionRequest` — 请求（UpdateSession）
定义于 `shell.go`。
- `Id` `string` json:`id` — Unique identifier of the target shell session
- `NoChangeTimeout` `*int` json:`no_change_timeout,omitempty` — New timeout (seconds) for detecting no new output from commands.

##### `ShellViewRequest` — 请求（View）
定义于 `shell.go`。
- `Id` `string` json:`id` — Unique identifier of the target shell session

##### `ShellWaitRequest` — 请求（WaitForProcess）
定义于 `shell.go`。
- `Id` `string` json:`id` — Unique identifier of the target shell session
- `Seconds` `*int` json:`seconds,omitempty` — Wait time (seconds)
- `MaxWaitSeconds` `*int` json:`max_wait_seconds,omitempty` — Maximum wait time (seconds) for the command to complete

##### `ShellWriteToProcessRequest` — 请求（WriteToProcess）
定义于 `shell.go`。
- `Id` `string` json:`id` — Unique identifier of the target shell session
- `Input` `string` json:`input` — Input content to write to the process
- `PressEnter` `bool` json:`press_enter` — Whether to press enter key after input

- `Response` — 响应（UpdateSession, CleanupAllSessions, CleanupSession）：共享类型（types.go），字段见 §3.0。

##### `ResponseActiveShellSessionsResult` — 响应（ListSessions）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ActiveShellSessionsResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellCommandResult` — 响应（ExecCommand）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellCommandResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellCreateSessionResponse` — 响应（CreateSession）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellCreateSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellKillResult` — 响应（KillProcess）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellKillResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellSessionStats` — 响应（GetSessionStats）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellSessionStats` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellViewResult` — 响应（View）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellViewResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellWaitResult` — 响应（WaitForProcess）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellWaitResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseShellWriteResult` — 响应（WriteToProcess）
定义于 `shell.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ShellWriteResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

- `ResponseStr` — 响应（GetTerminalUrl）：共享类型（types.go），字段见 §3.0。

**嵌套类型**（响应/请求内引用）

##### `ActiveShellSessionsResult` — 嵌套（被 `ResponseActiveShellSessionsResult.Data` 引用）
定义于 `shell.go`。
- `Sessions` `map[string]*ShellSessionInfo` json:`sessions` — Map of session ID to session info

##### `ShellCommandResult` — 嵌套（被 `ResponseShellCommandResult.Data` 引用）
定义于 `shell.go`。
- `SessionId` `string` json:`session_id` — Shell session ID
- `Command` `string` json:`command` — Executed command
- `Status` `BashCommandStatus` json:`status` — Command execution status
- `Output` `*string` json:`output,omitempty` — Command execution output, only has value when status is completed
- `Console` `[]*ConsoleRecord` json:`console,omitempty` — Console command records
- `ExitCode` `*int` json:`exit_code,omitempty` — Command execution exit code, only has value when status is completed

##### `ShellCreateSessionResponse` — 嵌套（被 `ResponseShellCreateSessionResponse.Data` 引用）
定义于 `shell.go`。
- `SessionId` `string` json:`session_id` — Unique identifier of the created shell session
- `WorkingDir` `string` json:`working_dir` — Working directory of the created session

##### `ShellKillResult` — 嵌套（被 `ResponseShellKillResult.Data` 引用）
定义于 `shell.go`。
- `Status` `BashCommandStatus` json:`status` — Process status
- `ExitCode` `*int` json:`exit_code,omitempty` — Process exit code before termination, None if process was still running
- `Returncode` `*int` json:`returncode,omitempty` — Deprecated: use exit_code instead.

##### `ShellSessionStats` — 嵌套（被 `ResponseShellSessionStats.Data` 引用）
定义于 `shell.go`。
- `TotalSessions` `int` json:`total_sessions` — Total number of sessions
- `ActiveSessions` `int` json:`active_sessions` — Number of active sessions (used within last 5 minutes)
- `IdleSessions` `int` json:`idle_sessions` — Number of idle sessions
- `MaxSessions` `int` json:`max_sessions` — Maximum allowed sessions
- `SessionTimeout` `int` json:`session_timeout` — Session timeout in seconds
- `UsageRatio` `float64` json:`usage_ratio` — Session usage ratio (0.0 to 1.0)

##### `ShellViewResult` — 嵌套（被 `ResponseShellViewResult.Data` 引用）
定义于 `shell.go`。
- `Output` `string` json:`output` — Shell session output content
- `SessionId` `string` json:`session_id` — Shell session ID
- `Console` `[]*ConsoleRecord` json:`console,omitempty` — Console command records
- `Status` `BashCommandStatus` json:`status` — Shell session status
- `Command` `*string` json:`command,omitempty` — Last executed or currently executing command
- `ExitCode` `*int` json:`exit_code,omitempty` — Command execution exit code, only has value when status is completed

##### `ShellWaitResult` — 嵌套（被 `ResponseShellWaitResult.Data` 引用）
定义于 `shell.go`。
- `Status` `BashCommandStatus` json:`status` — Process status

##### `ShellWriteResult` — 嵌套（被 `ResponseShellWriteResult.Data` 引用）
定义于 `shell.go`。
- `Status` `BashCommandStatus` json:`status` — Write status

##### `ShellSessionInfo` — 嵌套（被 `ActiveShellSessionsResult.Sessions` 引用）
定义于 `shell.go`。
- `WorkingDir` `string` json:`working_dir` — Working directory
- `CreatedAt` `time.Time` json:`created_at` — Creation timestamp
- `LastUsedAt` `time.Time` json:`last_used_at` — Last used timestamp
- `AgeSeconds` `int` json:`age_seconds` — Age of session in seconds
- `Status` `string` json:`status` — Session status
- `CurrentCommand` `*string` json:`current_command,omitempty` — Currently executing command

##### `ConsoleRecord` — 嵌套（被 `ShellCommandResult.Console` 引用）
定义于 `shell.go`。
- `Ps1` `string` json:`ps1` — Command prompt
- `Command` `string` json:`command` — Executed command
- `Output` `*string` json:`output,omitempty` — Command output

##### `BashCommandStatus` — 嵌套（被 `ShellCommandResult.Status` 引用）
> Shell command execution status (compatible with OpenHands)
`type BashCommandStatus string` — 枚举值：`BashCommandStatusRunning` = "running", `BashCommandStatusCompleted` = "completed", `BashCommandStatusNoChangeTimeout` = "no_change_timeout", `BashCommandStatusHardTimeout` = "hard_timeout", `BashCommandStatusTerminated` = "terminated"


---

### 3.3 `bash` — `client.Bash`

> bash 命令执行：同步/异步执行、增量输出轮询、写 stdin、发信号、会话管理

- 构造：`bash.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Bash.WithRawResponse`。
- 方法数：7；涉及类型：16（直接 10 + 嵌套 6）。

**方法（7）**

- `func (c *Client) Exec(ctx context.Context, request *sandboxsdkgo.BashExecRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseBashExecResult, error)` — Execute a bash command.
- `func (c *Client) Output(ctx context.Context, request *sandboxsdkgo.BashOutputRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseBashOutputResult, error)` — Read output from a bash session using offset-based streaming.
- `func (c *Client) Write(ctx context.Context, request *sandboxsdkgo.BashWriteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Write input to a bash session's stdin.
- `func (c *Client) Kill(ctx context.Context, request *sandboxsdkgo.BashKillRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Send a signal to a bash session's process.
- `func (c *Client) Sessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListBashSessionInfo, error)` — List all active bash sessions.
- `func (c *Client) CreateSession(ctx context.Context, request *sandboxsdkgo.BashSessionCreateRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseBashSessionInfo, error)` — Create a new bash session.
- `func (c *Client) CloseSession(ctx context.Context, sessionId string, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Close a bash session.

**类型**

##### `BashExecRequest` — 请求（Exec）
定义于 `bash.go`。
- `SessionId` `*string` json:`session_id,omitempty` — Target session ID.
- `Command` `string` json:`command` — Shell command to execute
- `ExecDir` `*string` json:`exec_dir,omitempty` — Working directory (absolute path).
- `Env` `map[string]*string` json:`env,omitempty` — Extra environment variables to inject for this command only.
- `AsyncMode` `*bool` json:`async_mode,omitempty` — If true, return immediately with running status.
- `Timeout` `*float64` json:`timeout,omitempty` — HTTP timeout (seconds).
- `HardTimeout` `*float64` json:`hard_timeout,omitempty` — Hard execution timeout (seconds).
- `MaxOutputLength` `*int` json:`max_output_length,omitempty` — Maximum character length for stdout/stderr in the response.

##### `BashKillRequest` — 请求（Kill）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Target session ID
- `Signal` `*string` json:`signal,omitempty` — Signal to send: SIGTERM, SIGKILL, or SIGINT

##### `BashOutputRequest` — 请求（Output）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Target session ID
- `CommandId` `*string` json:`command_id,omitempty` — Target a specific async command.
- `Offset` `*int` json:`offset,omitempty` — Stdout byte offset to read from
- `StderrOffset` `*int` json:`stderr_offset,omitempty` — Stderr byte offset to read from
- `Wait` `*bool` json:`wait,omitempty` — If true, long-poll until new output is available or wait_timeout is reached.
- `WaitTimeout` `*float64` json:`wait_timeout,omitempty` — Max seconds to wait for new output when wait=true.

##### `BashSessionCreateRequest` — 请求（CreateSession）
定义于 `bash.go`。
- `SessionId` `*string` json:`session_id,omitempty` — Session ID.
- `ExecDir` `*string` json:`exec_dir,omitempty` — Working directory for the new session (absolute path)
- `SnapshotPath` `*string` json:`snapshot_path,omitempty` — Path to a shell snapshot script to source on session init

##### `BashWriteRequest` — 请求（Write）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Target session ID
- `CommandId` `*string` json:`command_id,omitempty` — Target a specific async command.
- `Input` `string` json:`input` — Content to write to the process stdin

- `Response` — 响应（Write, Kill, CloseSession）：共享类型（types.go），字段见 §3.0。

##### `ResponseBashExecResult` — 响应（Exec）
定义于 `bash.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*BashExecResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseBashOutputResult` — 响应（Output）
定义于 `bash.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*BashOutputResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseBashSessionInfo` — 响应（CreateSession）
定义于 `bash.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*BashSessionInfo` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseListBashSessionInfo` — 响应（Sessions）
定义于 `bash.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `[]*BashSessionInfo` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `BashExecResult` — 嵌套（被 `ResponseBashExecResult.Data` 引用）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Session identifier
- `CommandId` `string` json:`command_id` — Unique command identifier
- `Command` `string` json:`command` — The executed command
- `Status` `CommandStatus` json:`status` — Command status
- `Stdout` `*string` json:`stdout,omitempty` — Stdout output up to this point
- `Stderr` `*string` json:`stderr,omitempty` — Stderr output up to this point
- `ExitCode` `*int` json:`exit_code,omitempty` — Exit code (when completed)
- `Offset` `*int` json:`offset,omitempty` — Current stdout offset for subsequent /output calls
- `StderrOffset` `*int` json:`stderr_offset,omitempty` — Current stderr offset

##### `BashOutputResult` — 嵌套（被 `ResponseBashOutputResult.Data` 引用）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Session identifier
- `Stdout` `*string` json:`stdout,omitempty` — New stdout data since last offset
- `Stderr` `*string` json:`stderr,omitempty` — New stderr data since last offset
- `Offset` `*int` json:`offset,omitempty` — Current stdout offset (use for next request)
- `StderrOffset` `*int` json:`stderr_offset,omitempty` — Current stderr offset (use for next request)
- `Command` `*BashCommandInfo` json:`command,omitempty` — Current or most recent command status

##### `BashSessionInfo` — 嵌套（被 `ResponseBashSessionInfo.Data` 引用）
定义于 `bash.go`。
- `SessionId` `string` json:`session_id` — Session identifier
- `Status` `SessionStatus` json:`status` — Session status
- `WorkingDir` `string` json:`working_dir` — Working directory
- `CreatedAt` `time.Time` json:`created_at` — Creation timestamp
- `LastUsedAt` `time.Time` json:`last_used_at` — Last used timestamp
- `CurrentCommand` `*string` json:`current_command,omitempty` — Currently executing command
- `CommandCount` `*int` json:`command_count,omitempty` — Total commands executed

##### `CommandStatus` — 嵌套（被 `BashExecResult.Status` 引用）
> Status of a bash command execution.
`type CommandStatus string` — 枚举值：`CommandStatusPending` = "pending", `CommandStatusRunning` = "running", `CommandStatusCompleted` = "completed", `CommandStatusTimedOut` = "timed_out", `CommandStatusKilled` = "killed"

##### `BashCommandInfo` — 嵌套（被 `BashOutputResult.Command` 引用）
定义于 `bash.go`。
- `CommandId` `string` json:`command_id` — Unique command identifier
- `Command` `string` json:`command` — The command string
- `Status` `CommandStatus` json:`status` — Command execution status
- `ExitCode` `*int` json:`exit_code,omitempty` — Exit code (when completed)

##### `SessionStatus` — 嵌套（被 `BashSessionInfo.Status` 引用）
> Status of a pipe bash session.
`type SessionStatus string` — 枚举值：`SessionStatusReady` = "ready", `SessionStatusClosed` = "closed"


---

### 3.4 `file` — `client.File`

> 文件系统：读/写/替换/搜索、find/grep/glob、上传/下载、列目录、字符串替换编辑器、文件监视 watch

- 构造：`file.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.File.WithRawResponse`。
- 方法数：17；涉及类型：41（直接 24 + 嵌套 17）。

**方法（17）**

- `func (c *Client) ReadFile(ctx context.Context, request *sandboxsdkgo.FileReadRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileReadResult, error)` — Read file content
- `func (c *Client) WriteFile(ctx context.Context, request *sandboxsdkgo.FileWriteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileWriteResult, error)` — Write file content (supports both text and binary files) For binary files, set encoding to 'base64' and provide base64-encoded content.
- `func (c *Client) ReplaceInFile(ctx context.Context, request *sandboxsdkgo.FileReplaceRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileReplaceResult, error)` — Replace string in file
- `func (c *Client) SearchInFile(ctx context.Context, request *sandboxsdkgo.FileSearchRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileSearchResult, error)` — Search in file content
- `func (c *Client) FindFiles(ctx context.Context, request *sandboxsdkgo.FileFindRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileFindResult, error)` — Find files by name pattern
- `func (c *Client) GrepFiles(ctx context.Context, request *sandboxsdkgo.FileGrepRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileGrepResult, error)` — Multi-file content search (grep) with regex or fixed string support
- `func (c *Client) GlobFiles(ctx context.Context, request *sandboxsdkgo.FileGlobRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileGlobResult, error)` — Enhanced file glob matching with optional metadata
- `func (c *Client) UploadFile(ctx context.Context, request *sandboxsdkgo.BodyUploadFile, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileUploadResult, error)` — Upload file using streaming
- `func (c *Client) DownloadFile(ctx context.Context, request *sandboxsdkgo.FileDownloadFileRequest, opts ...option.RequestOption) -> (io.Reader, error)` — Download file using FileResponse
- `func (c *Client) ListPath(ctx context.Context, request *sandboxsdkgo.FileListRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseFileListResult, error)` — List path contents with flexible options
- `func (c *Client) StrReplaceEditor(ctx context.Context, request *sandboxsdkgo.StrReplaceEditorRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseStrReplaceEditorResult, error)` — An filesystem editor tool that allows the agent to - view - create - navigate - edit files The tool parameters are defined by Anthropic and are not editable.
- `func (c *Client) WatchList(ctx context.Context, opts ...option.RequestOption) -> (any, error)`
- `func (c *Client) WatchCreate(ctx context.Context, request *sandboxsdkgo.CreateWatchRequest, opts ...option.RequestOption) -> (any, error)`
- `func (c *Client) WatchEvents(ctx context.Context, watcherId string, opts ...option.RequestOption) -> (any, error)`
- `func (c *Client) WatchPoll(ctx context.Context, watcherId string, request *sandboxsdkgo.PollRequest, opts ...option.RequestOption) -> (any, error)`
- `func (c *Client) WatchWait(ctx context.Context, request *sandboxsdkgo.FileWatchWaitRequest, opts ...option.RequestOption) -> (any, error)`
- `func (c *Client) WatchStop(ctx context.Context, watcherId string, opts ...option.RequestOption) -> (any, error)`

**类型**

##### `BodyUploadFile` — 请求（UploadFile）
定义于 `file.go`。
- `File` `io.Reader` json:`-`
- `Path` `*string` json:`path,omitempty`

##### `CreateWatchRequest` — 请求（WatchCreate）
定义于 `file.go`。
- `Path` `string` json:`path` — 监听目录或文件路径
- `Recursive` `*bool` json:`recursive,omitempty` — 是否递归子目录
- `Exclude` `[]string` json:`exclude,omitempty` — 排除的目录/glob 模式
- `Debounce` `*int` json:`debounce,omitempty` — 去抖动窗口(ms)
- `IncludePatterns` `[]string` json:`include_patterns,omitempty` — glob 过滤，空=全部通过

##### `FileDownloadFileRequest` — 请求（DownloadFile）
定义于 `file.go`。
- `Path` `string` json:`-`

##### `FileFindRequest` — 请求（FindFiles）
定义于 `file.go`。
- `Path` `string` json:`path` — Directory path to search
- `Glob` `string` json:`glob` — Filename pattern (glob syntax)

##### `FileGlobRequest` — 请求（GlobFiles）
定义于 `file.go`。
- `Path` `string` json:`path` — Base directory path
- `Pattern` `string` json:`pattern` — Glob pattern (**, *, ?, [...])
- `Exclude` `[]string` json:`exclude,omitempty` — Glob patterns to exclude
- `IncludeHidden` `*bool` json:`include_hidden,omitempty` — Whether to include hidden files
- `FilesOnly` `*bool` json:`files_only,omitempty` — Only return files (not directories)
- `IncludeMetadata` `*bool` json:`include_metadata,omitempty` — Whether to include size and modified time
- `MaxResults` `*int` json:`max_results,omitempty` — Maximum number of results
- `SortBy` `*string` json:`sort_by,omitempty` — Sort by: path, name, size, modified
- `SortDesc` `*bool` json:`sort_desc,omitempty` — Sort in descending order

##### `FileGrepRequest` — 请求（GrepFiles）
定义于 `file.go`。
- `Path` `string` json:`path` — File or directory path to search
- `Pattern` `string` json:`pattern` — Search pattern (regex or fixed string)
- `Include` `[]string` json:`include,omitempty` — File glob filters to include (e.g., ["*.py", "*.ts"])
- `Exclude` `[]string` json:`exclude,omitempty` — Glob patterns to exclude (e.g., ["node_modules", "*.min.js"])
- `CaseInsensitive` `*bool` json:`case_insensitive,omitempty` — Case insensitive search
- `FixedStrings` `*bool` json:`fixed_strings,omitempty` — Treat pattern as literal string, not regex
- `ContextBefore` `*int` json:`context_before,omitempty` — Number of lines before each match (-B)
- `ContextAfter` `*int` json:`context_after,omitempty` — Number of lines after each match (-A)
- `MaxResults` `*int` json:`max_results,omitempty` — Maximum number of matches to return
- `MaxFileSize` `*string` json:`max_file_size,omitempty` — Skip files larger than this size (e.g., 1M, 500K)
- `Multiline` `*bool` json:`multiline,omitempty` — Enable multiline matching where .
- `Offset` `*int` json:`offset,omitempty` — Skip first N matches before returning results (for pagination)
- `Type` `*string` json:`type,omitempty` — File type filter using ripgrep type aliases (e.g., "py", "js", "rust", "go").
- `Recursive` `*bool` json:`recursive,omitempty` — Search recursively

##### `FileListRequest` — 请求（ListPath）
定义于 `file.go`。
- `Path` `string` json:`path` — Directory path to list
- `Recursive` `*bool` json:`recursive,omitempty` — Whether to list recursively
- `ShowHidden` `*bool` json:`show_hidden,omitempty` — Whether to show hidden files
- `FileTypes` `[]string` json:`file_types,omitempty` — Filter by file extensions (e.g., ['.py', '.txt'])
- `MaxDepth` `*int` json:`max_depth,omitempty` — Maximum depth for recursive listing
- `IncludeSize` `*bool` json:`include_size,omitempty` — Whether to include file size information
- `IncludePermissions` `*bool` json:`include_permissions,omitempty` — Whether to include file permissions
- `SortBy` `*string` json:`sort_by,omitempty` — Sort by: name, size, modified, type
- `SortDesc` `*bool` json:`sort_desc,omitempty` — Sort in descending order

##### `FileReadRequest` — 请求（ReadFile）
定义于 `file.go`。
- `File` `string` json:`file` — Absolute file path
- `StartLine` `*int` json:`start_line,omitempty` — Start line (0-based)
- `EndLine` `*int` json:`end_line,omitempty` — End line (not inclusive)
- `Sudo` `*bool` json:`sudo,omitempty` — Whether to use sudo privileges

##### `FileReplaceRequest` — 请求（ReplaceInFile）
定义于 `file.go`。
- `File` `string` json:`file` — Absolute file path
- `OldStr` `string` json:`old_str` — Original string to replace
- `NewStr` `string` json:`new_str` — New string to replace with
- `Sudo` `*bool` json:`sudo,omitempty` — Whether to use sudo privileges

##### `FileSearchRequest` — 请求（SearchInFile）
定义于 `file.go`。
- `File` `string` json:`file` — Absolute file path
- `Regex` `string` json:`regex` — Regular expression pattern
- `Sudo` `*bool` json:`sudo,omitempty` — Whether to use sudo privileges

##### `FileWatchWaitRequest` — 请求（WatchWait）
定义于 `file.go`。
- `Path` `string` json:`path` — 等待的文件路径（精确匹配）
- `Timeout` `*int` json:`timeout,omitempty` — 最大等待秒数
- `EventTypes` `[]AppSchemasFileWatchWaitRequestEventTypesItem` json:`event_types,omitempty` — 关注的事件类型

##### `FileWriteRequest` — 请求（WriteFile）
定义于 `file.go`。
- `File` `string` json:`file` — Absolute file path
- `Content` `string` json:`content` — Content to write (text or base64 encoded for binary)
- `Encoding` `*FileContentEncoding` json:`encoding,omitempty` — Content encoding: utf-8 for text, base64 for binary data
- `Append` `*bool` json:`append,omitempty` — Whether to use append mode
- `LeadingNewline` `*bool` json:`leading_newline,omitempty` — Whether to add leading newline (only for text mode)
- `TrailingNewline` `*bool` json:`trailing_newline,omitempty` — Whether to add trailing newline (only for text mode)
- `Sudo` `*bool` json:`sudo,omitempty` — Whether to use sudo privileges

##### `PollRequest` — 请求（WatchPoll）
定义于 `file.go`。
- `Cursor` `*int` json:`cursor,omitempty` — 上次返回的游标值，只返回 seq > cursor 的事件
- `Limit` `*int` json:`limit,omitempty` — 最多返回条数
- `Timeout` `*int` json:`timeout,omitempty` — 长轮询等待秒数，0=立即返回

##### `StrReplaceEditorRequest` — 请求（StrReplaceEditor）
定义于 `file.go`。
- `Command` `Command` json:`command` — The commands to run.
- `Path` `string` json:`path` — Absolute path to file or directory, e.g. /workspace/file.py or /workspace.
- `FileText` `*string` json:`file_text,omitempty` — Required parameter of create command, with the content of the file to be created.
- `OldStr` `*string` json:`old_str,omitempty` — Required parameter of str_replace command containing the string in path to replace.
- `NewStr` `*string` json:`new_str,omitempty` — Optional parameter of str_replace command containing the new string (if not given, no string will be added).
- `InsertLine` `*int` json:`insert_line,omitempty` — Required parameter of insert command.
- `ViewRange` `[]int` json:`view_range,omitempty` — Optional parameter of view command when path points to a file.
- `ReplaceMode` `*StrReplaceEditorRequestReplaceMode` json:`replace_mode,omitempty` — Optional parameter of str_replace command.
- `PageRange` `[]int` json:`page_range,omitempty` — Optional parameter for view command on PDF files.
- `SheetName` `*string` json:`sheet_name,omitempty` — Optional parameter for view command on Excel files.
- `RowRange` `[]int` json:`row_range,omitempty` — Optional parameter for view command on Excel files.
- `SlideRange` `[]int` json:`slide_range,omitempty` — Optional parameter for view command on PPTX files.
- `EnableMetadata` `*bool` json:`enable_metadata,omitempty` — Optional parameter for view command.

##### `ResponseFileFindResult` — 响应（FindFiles）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileFindResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileGlobResult` — 响应（GlobFiles）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileGlobResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileGrepResult` — 响应（GrepFiles）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileGrepResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileListResult` — 响应（ListPath）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileListResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileReadResult` — 响应（ReadFile）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileReadResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileReplaceResult` — 响应（ReplaceInFile）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileReplaceResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileSearchResult` — 响应（SearchInFile）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileSearchResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileUploadResult` — 响应（UploadFile）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileUploadResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseFileWriteResult` — 响应（WriteFile）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*FileWriteResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseStrReplaceEditorResult` — 响应（StrReplaceEditor）
定义于 `file.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*StrReplaceEditorResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `AppSchemasFileWatchWaitRequestEventTypesItem` — 嵌套（被 `FileWatchWaitRequest.EventTypes` 引用）
`type AppSchemasFileWatchWaitRequestEventTypesItem string` — 枚举值：`AppSchemasFileWatchWaitRequestEventTypesItemCreate` = "create", `AppSchemasFileWatchWaitRequestEventTypesItemWrite` = "write", `AppSchemasFileWatchWaitRequestEventTypesItemRemove` = "remove", `AppSchemasFileWatchWaitRequestEventTypesItemRename` = "rename", `AppSchemasFileWatchWaitRequestEventTypesItemChmod` = "chmod"

##### `FileContentEncoding` — 嵌套（被 `FileWriteRequest.Encoding` 引用）
> File content encoding type
`type FileContentEncoding string` — 枚举值：`FileContentEncodingUtf8` = "utf-8", `FileContentEncodingBase64` = "base64", `FileContentEncodingRaw` = "raw"

##### `FileFindResult` — 嵌套（被 `ResponseFileFindResult.Data` 引用）
定义于 `file.go`。
- `Path` `string` json:`path` — Path of the search directory
- `Files` `[]string` json:`files,omitempty` — List of found files

##### `FileGlobResult` — 嵌套（被 `ResponseFileGlobResult.Data` 引用）
定义于 `file.go`。
- `Path` `string` json:`path` — Base directory path
- `Pattern` `string` json:`pattern` — Glob pattern used
- `Files` `[]*GlobFileInfo` json:`files,omitempty` — List of matched files
- `TotalCount` `*int` json:`total_count,omitempty` — Total number of matches
- `Truncated` `*bool` json:`truncated,omitempty` — Whether results were truncated

##### `FileGrepResult` — 嵌套（被 `ResponseFileGrepResult.Data` 引用）
定义于 `file.go`。
- `Path` `string` json:`path` — Search directory path
- `Pattern` `string` json:`pattern` — Search pattern used
- `Matches` `[]*GrepMatch` json:`matches,omitempty` — List of matches
- `MatchCount` `*int` json:`match_count,omitempty` — Total number of matches
- `FilesSearched` `*int` json:`files_searched,omitempty` — Number of files searched
- `FilesMatched` `*int` json:`files_matched,omitempty` — Number of files with matches
- `Truncated` `*bool` json:`truncated,omitempty` — Whether results were truncated

##### `FileListResult` — 嵌套（被 `ResponseFileListResult.Data` 引用）
定义于 `file.go`。
- `Path` `string` json:`path` — Listed directory path
- `Files` `[]*FileInfo` json:`files,omitempty` — List of files and directories
- `TotalCount` `*int` json:`total_count,omitempty` — Total number of items
- `DirectoryCount` `*int` json:`directory_count,omitempty` — Number of directories
- `FileCount` `*int` json:`file_count,omitempty` — Number of files

##### `FileReadResult` — 嵌套（被 `ResponseFileReadResult.Data` 引用）
定义于 `file.go`。
- `Content` `string` json:`content` — File content
- `File` `string` json:`file` — Path of the read file

##### `FileReplaceResult` — 嵌套（被 `ResponseFileReplaceResult.Data` 引用）
定义于 `file.go`。
- `File` `string` json:`file` — Path of the operated file
- `ReplacedCount` `*int` json:`replaced_count,omitempty` — Number of replacements

##### `FileSearchResult` — 嵌套（被 `ResponseFileSearchResult.Data` 引用）
定义于 `file.go`。
- `File` `string` json:`file` — Path of the searched file
- `Matches` `[]string` json:`matches,omitempty` — List of matched content
- `LineNumbers` `[]int` json:`line_numbers,omitempty` — List of matched line numbers

##### `FileUploadResult` — 嵌套（被 `ResponseFileUploadResult.Data` 引用）
定义于 `file.go`。
- `FilePath` `string` json:`file_path` — Path of the uploaded file
- `FileSize` `int` json:`file_size` — Size of the uploaded file in bytes
- `Success` `bool` json:`success` — Whether upload was successful

##### `FileWriteResult` — 嵌套（被 `ResponseFileWriteResult.Data` 引用）
定义于 `file.go`。
- `File` `string` json:`file` — Path of the written file
- `BytesWritten` `*int` json:`bytes_written,omitempty` — Number of bytes written

##### `StrReplaceEditorResult` — 嵌套（被 `ResponseStrReplaceEditorResult.Data` 引用）
定义于 `file.go`。
- `Output` `string` json:`output` — Command execution output
- `Error` `*string` json:`error,omitempty` — Error message if any
- `Path` `string` json:`path` — File path that was operated on
- `PrevExist` `bool` json:`prev_exist` — Whether the file existed before operation
- `OldContent` `*string` json:`old_content,omitempty` — Previous file content
- `NewContent` `*string` json:`new_content,omitempty` — New file content after operation
- `Metadata` `map[string]interface{}` json:`metadata,omitempty` — File metadata (only returned when enable_metadata=true for binary files)

##### `StrReplaceEditorRequestReplaceMode` — 嵌套（被 `StrReplaceEditorRequest.ReplaceMode` 引用）
`type StrReplaceEditorRequestReplaceMode string` — 枚举值：`StrReplaceEditorRequestReplaceModeAll` = "ALL", `StrReplaceEditorRequestReplaceModeFirst` = "FIRST", `StrReplaceEditorRequestReplaceModeLast` = "LAST"

##### `Command` — 嵌套（被 `StrReplaceEditorRequest.Command` 引用）
> The commands to run.
`type Command string` — 枚举值：`CommandView` = "view", `CommandCreate` = "create", `CommandStrReplace` = "str_replace", `CommandInsert` = "insert", `CommandUndoEdit` = "undo_edit"

##### `GlobFileInfo` — 嵌套（被 `FileGlobResult.Files` 引用）
定义于 `file.go`。
- `Path` `string` json:`path` — Full file path
- `Name` `string` json:`name` — File name
- `IsDirectory` `*bool` json:`is_directory,omitempty` — Whether it's a directory
- `Size` `*int` json:`size,omitempty` — File size in bytes
- `ModifiedTime` `*string` json:`modified_time,omitempty` — Last modified time (ISO format)

##### `GrepMatch` — 嵌套（被 `FileGrepResult.Matches` 引用）
定义于 `file.go`。
- `File` `string` json:`file` — File path containing the match
- `LineNumber` `int` json:`line_number` — Line number (1-based)
- `LineContent` `string` json:`line_content` — Content of the matched line
- `ContextBefore` `[]string` json:`context_before,omitempty` — Lines before the match
- `ContextAfter` `[]string` json:`context_after,omitempty` — Lines after the match

##### `FileInfo` — 嵌套（被 `FileListResult.Files` 引用）
定义于 `file.go`。
- `Name` `string` json:`name` — File name
- `Path` `string` json:`path` — Full file path
- `IsDirectory` `bool` json:`is_directory` — Whether it's a directory
- `Size` `*int` json:`size,omitempty` — File size in bytes
- `ModifiedTime` `*string` json:`modified_time,omitempty` — Last modified time (ISO format)
- `Permissions` `*string` json:`permissions,omitempty` — File permissions
- `Extension` `*string` json:`extension,omitempty` — File extension


---

### 3.5 `jupyter` — `client.Jupyter`

> Jupyter：创建会话、执行代码、内核信息、会话列举/删除

- 构造：`jupyter.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Jupyter.WithRawResponse`。
- 方法数：6；涉及类型：13（直接 7 + 嵌套 6）。

**方法（6）**

- `func (c *Client) ExecuteCode(ctx context.Context, request *sandboxsdkgo.JupyterExecuteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseJupyterExecuteResponse, error)` — Execute Python code using Jupyter kernel with session persistence This endpoint allows you to execute Python code and get results back.
- `func (c *Client) GetInfo(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseJupyterInfoResponse, error)` — Get information about available Jupyter kernels
- `func (c *Client) ListSessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseActiveSessionsResult, error)` — List all active Jupyter sessions
- `func (c *Client) DeleteSessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Cleanup all active sessions
- `func (c *Client) DeleteSession(ctx context.Context, sessionId string, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Manually cleanup a specific session
- `func (c *Client) CreateSession(ctx context.Context, request *sandboxsdkgo.JupyterCreateSessionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseJupyterCreateSessionResponse, error)` — Create a new Jupyter session

**类型**

##### `JupyterCreateSessionRequest` — 请求（CreateSession）
定义于 `jupyter.go`。
- `SessionId` `*string` json:`session_id,omitempty` — Unique identifier for the session, auto-generated if not provided
- `KernelName` `*string` json:`kernel_name,omitempty` — Kernel name: 'python3', 'python3.10', 'python3.11', 'python3.12'.
- `Cwd` `*string` json:`cwd,omitempty` — Current working directory for the session

##### `JupyterExecuteRequest` — 请求（ExecuteCode）
定义于 `jupyter.go`。
- `Code` `string` json:`code` — Python code to execute
- `Timeout` `*int` json:`timeout,omitempty` — Execution timeout in seconds
- `KernelName` `*string` json:`kernel_name,omitempty` — Kernel name: 'python3', 'python3.10', 'python3.11', 'python3.12'.
- `SessionId` `*string` json:`session_id,omitempty` — Session ID to maintain kernel state across requests
- `Cwd` `*string` json:`cwd,omitempty` — Current working directory for the kernel

- `Response` — 响应（DeleteSessions, DeleteSession）：共享类型（types.go），字段见 §3.0。

##### `ResponseActiveSessionsResult` — 响应（ListSessions）
定义于 `jupyter.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ActiveSessionsResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseJupyterCreateSessionResponse` — 响应（CreateSession）
定义于 `jupyter.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*JupyterCreateSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseJupyterExecuteResponse` — 响应（ExecuteCode）
定义于 `jupyter.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*JupyterExecuteResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseJupyterInfoResponse` — 响应（GetInfo）
定义于 `jupyter.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*JupyterInfoResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `ActiveSessionsResult` — 嵌套（被 `ResponseActiveSessionsResult.Data` 引用）
定义于 `jupyter.go`。
- `Sessions` `map[string]*SessionInfo` json:`sessions` — Map of session ID to session info

##### `JupyterCreateSessionResponse` — 嵌套（被 `ResponseJupyterCreateSessionResponse.Data` 引用）
定义于 `jupyter.go`。
- `SessionId` `string` json:`session_id` — Unique identifier of the created session
- `KernelName` `string` json:`kernel_name` — Name of the kernel associated with the session
- `Message` `string` json:`message` — Status message about session creation

##### `JupyterExecuteResponse` — 嵌套（被 `ResponseJupyterExecuteResponse.Data` 引用）
定义于 `jupyter.go`。
- `KernelName` `string` json:`kernel_name` — Name of the kernel used for execution
- `SessionId` `*string` json:`session_id,omitempty` — Session ID for this kernel instance
- `Status` `string` json:`status` — Execution status: ok, error, or timeout
- `ExecutionCount` `*int` json:`execution_count,omitempty` — Execution count from the kernel
- `Outputs` `[]*JupyterOutput` json:`outputs` — List of execution outputs
- `Code` `string` json:`code` — The executed code
- `MsgId` `*string` json:`msg_id,omitempty` — Message ID from Jupyter kernel

##### `JupyterInfoResponse` — 嵌套（被 `ResponseJupyterInfoResponse.Data` 引用）
定义于 `jupyter.go`。
- `DefaultKernel` `string` json:`default_kernel` — Default kernel name
- `AvailableKernels` `[]string` json:`available_kernels` — List of available kernel names
- `ActiveSessions` `int` json:`active_sessions` — Number of active sessions
- `SessionTimeoutSeconds` `int` json:`session_timeout_seconds` — Session timeout in seconds
- `MaxSessions` `int` json:`max_sessions` — Maximum number of concurrent sessions
- `Description` `string` json:`description` — Service description
- `KernelDetection` `string` json:`kernel_detection` — Kernel detection strategy

##### `SessionInfo` — 嵌套（被 `ActiveSessionsResult.Sessions` 引用）
定义于 `jupyter.go`。
- `KernelName` `string` json:`kernel_name` — Kernel name
- `LastUsed` `float64` json:`last_used` — Last used timestamp
- `AgeSeconds` `int` json:`age_seconds` — Age of session in seconds

##### `JupyterOutput` — 嵌套（被 `JupyterExecuteResponse.Outputs` 引用）
定义于 `jupyter.go`。
- `OutputType` `string` json:`output_type` — Type of output: stream, execute_result, display_data, or error
- `Name` `*string` json:`name,omitempty` — Stream name (stdout/stderr) for stream outputs
- `Text` `*string` json:`text,omitempty` — Text content for stream outputs
- `Data` `map[string]interface{}` json:`data,omitempty` — Output data for execute_result/display_data
- `Metadata` `map[string]interface{}` json:`metadata,omitempty` — Output metadata
- `ExecutionCount` `*int` json:`execution_count,omitempty` — Execution count for execute_result
- `Ename` `*string` json:`ename,omitempty` — Error name for error outputs
- `Evalue` `*string` json:`evalue,omitempty` — Error value for error outputs
- `Traceback` `[]string` json:`traceback,omitempty` — Error traceback for error outputs


---

### 3.6 `nodejs` — `client.Nodejs`

> Node.js：创建会话、执行代码、运行时信息、会话 CRUD

- 构造：`nodejs.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Nodejs.WithRawResponse`。
- 方法数：7；涉及类型：20（直接 10 + 嵌套 10）。

**方法（7）**

- `func (c *Client) ExecuteCode(ctx context.Context, request *sandboxsdkgo.NodeJsExecuteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsExecuteResponse, error)` — Execute JavaScript code using Node.js This endpoint allows you to execute JavaScript code and get results back.
- `func (c *Client) GetInfo(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsRuntimeInfo, error)` — Get information about Node.js REPL runtime, including installed packages Returns Node.js version, npm version, and lists of installed packages from both the runtime direc…
- `func (c *Client) ListSessions(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsSessionListResponse, error)` — List all active Node.js REPL sessions Returns information about all active sessions including their state, working directory, and idle time.
- `func (c *Client) CreateSession(ctx context.Context, request *sandboxsdkgo.NodeJsCreateSessionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsCreateSessionResponse, error)` — Create a new Node.js REPL session Creates a new persistent REPL session with configurable working directory and idle timeout.
- `func (c *Client) GetSession(ctx context.Context, sessionId string, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsSessionResponse, error)` — Get information about a specific Node.js REPL session Returns detailed information about a session including its state, working directory, creation time, and idle time.
- `func (c *Client) DeleteSession(ctx context.Context, sessionId string, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsDeleteSessionResponse, error)` — Delete a Node.js REPL session Terminates the session and releases all associated resources.
- `func (c *Client) UpdateSession(ctx context.Context, sessionId string, request *sandboxsdkgo.NodeJsUpdateSessionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseNodeJsUpdateSessionResponse, error)` — Update a Node.js REPL session configuration Updates session properties like maximum idle time or working directory.

**类型**

##### `NodeJsCreateSessionRequest` — 请求（CreateSession）
定义于 `nodejs.go`。
- `SessionId` `*string` json:`session_id,omitempty` — Custom session ID (auto-generated if not provided)
- `Cwd` `*string` json:`cwd,omitempty` — Working directory for the session
- `MaxIdleTime` `*int` json:`max_idle_time,omitempty` — Maximum idle time in seconds (default 24 hours)

##### `NodeJsExecuteRequest` — 请求（ExecuteCode）
定义于 `nodejs.go`。
- `Code` `string` json:`code` — JavaScript code to execute
- `Timeout` `*int` json:`timeout,omitempty` — Execution timeout in seconds
- `Stdin` `*string` json:`stdin,omitempty` — Standard input for the process
- `Files` `map[string]*string` json:`files,omitempty` — Additional files to create in execution directory
- `Stateful` `*bool` json:`stateful,omitempty` — Enable stateful execution with persistent REPL session
- `SessionId` `*string` json:`session_id,omitempty` — Session ID for stateful execution (reuse existing session)
- `Cwd` `*string` json:`cwd,omitempty` — Working directory for code execution
- `Version` `*string` json:`version,omitempty` — Node.js version to use: "node20", "node22", "node24", or aliases "20", "22", "24"

##### `NodeJsUpdateSessionRequest` — 请求（UpdateSession）
定义于 `nodejs.go`。
- `MaxIdleTime` `*int` json:`max_idle_time,omitempty` — New maximum idle time in seconds
- `Cwd` `*string` json:`cwd,omitempty` — New working directory

##### `ResponseNodeJsCreateSessionResponse` — 响应（CreateSession）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsCreateSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsDeleteSessionResponse` — 响应（DeleteSession）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsDeleteSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsExecuteResponse` — 响应（ExecuteCode）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsExecuteResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsRuntimeInfo` — 响应（GetInfo）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsRuntimeInfo` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsSessionListResponse` — 响应（ListSessions）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsSessionListResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsSessionResponse` — 响应（GetSession）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseNodeJsUpdateSessionResponse` — 响应（UpdateSession）
定义于 `nodejs.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*NodeJsUpdateSessionResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `NodeJsCreateSessionResponse` — 嵌套（被 `ResponseNodeJsCreateSessionResponse.Data` 引用）
定义于 `nodejs.go`。
- `SessionId` `string` json:`session_id` — Session ID
- `Created` `bool` json:`created` — Whether the session was newly created
- `Message` `*string` json:`message,omitempty` — Additional message (e.g., if session already exists)
- `Session` `*NodeJsSessionInfo` json:`session,omitempty` — Session information (if created)

##### `NodeJsDeleteSessionResponse` — 嵌套（被 `ResponseNodeJsDeleteSessionResponse.Data` 引用）
定义于 `nodejs.go`。
- `Deleted` `bool` json:`deleted` — Whether the session was deleted

##### `NodeJsExecuteResponse` — 嵌套（被 `ResponseNodeJsExecuteResponse.Data` 引用）
定义于 `nodejs.go`。
- `Status` `string` json:`status` — Language that was executed (always 'javascript') Execution status: ok, error, or timeout
- `ExecutionCount` `*int` json:`execution_count,omitempty` — Execution count
- `Outputs` `[]*NodeJsOutput` json:`outputs,omitempty` — List of execution outputs
- `Code` `string` json:`code` — Code that was executed
- `Stdout` `*string` json:`stdout,omitempty` — Standard output
- `Stderr` `*string` json:`stderr,omitempty` — Standard error
- `ExitCode` `int` json:`exit_code` — Process exit code
- `SessionId` `*string` json:`session_id,omitempty` — Session ID for stateful execution (use this to continue the session)

##### `NodeJsRuntimeInfo` — 嵌套（被 `ResponseNodeJsRuntimeInfo.Data` 引用）
定义于 `nodejs.go`。
- `NodeVersion` `string` json:`node_version` — Node.js version
- `NpmVersion` `string` json:`npm_version` — npm version
- `SupportedLanguages` `[]string` json:`supported_languages` — List of supported languages
- `Description` `string` json:`description` — Service description
- `RuntimeDirectory` `*string` json:`runtime_directory,omitempty` — Runtime directory path
- `GlobalNpmDirectory` `*string` json:`global_npm_directory,omitempty` — Global npm directory path
- `RuntimePackages` `[]*NodeJsPackageInfo` json:`runtime_packages,omitempty` — Pre-installed runtime packages
- `GlobalPackages` `[]*NodeJsPackageInfo` json:`global_packages,omitempty` — Globally installed npm packages
- `Error` `*string` json:`error,omitempty` — Error message if runtime info retrieval failed
- `AvailableVersions` `[]string` json:`available_versions,omitempty` — Available Node.js versions (e.g., node20, node22, node24)
- `CurrentVersion` `*string` json:`current_version,omitempty` — Currently active Node.js version

##### `NodeJsSessionListResponse` — 嵌套（被 `ResponseNodeJsSessionListResponse.Data` 引用）
定义于 `nodejs.go`。
- `Sessions` `map[string]*NodeJsSessionInfo` json:`sessions,omitempty` — Map of session ID to session info

##### `NodeJsSessionResponse` — 嵌套（被 `ResponseNodeJsSessionResponse.Data` 引用）
定义于 `nodejs.go`。
- `Session` `*NodeJsSessionInfo` json:`session` — Session information

##### `NodeJsUpdateSessionResponse` — 嵌套（被 `ResponseNodeJsUpdateSessionResponse.Data` 引用）
定义于 `nodejs.go`。
- `Updated` `bool` json:`updated` — Whether the update was successful
- `Session` `*NodeJsSessionInfo` json:`session` — Updated session information

##### `NodeJsSessionInfo` — 嵌套（被 `NodeJsCreateSessionResponse.Session` 引用）
定义于 `nodejs.go`。
- `SessionId` `string` json:`session_id` — Session ID
- `Cwd` `string` json:`cwd` — Working directory
- `CreatedAt` `float64` json:`created_at` — Session creation timestamp (ms since epoch)
- `LastUsed` `float64` json:`last_used` — Last activity timestamp (ms since epoch)
- `MaxIdleTime` `int` json:`max_idle_time` — Maximum idle time in milliseconds
- `AgeSeconds` `int` json:`age_seconds` — Seconds since last activity
- `State` `string` json:`state` — Session state: IDLE or EXECUTING

##### `NodeJsOutput` — 嵌套（被 `NodeJsExecuteResponse.Outputs` 引用）
定义于 `nodejs.go`。
- `OutputType` `string` json:`output_type` — Type of output: stream, error, or execute_result
- `Name` `*string` json:`name,omitempty` — Stream name (stdout/stderr) for stream outputs
- `Text` `*string` json:`text,omitempty` — Text content for stream outputs
- `Ename` `*string` json:`ename,omitempty` — Error name for error outputs
- `Evalue` `*string` json:`evalue,omitempty` — Error value for error outputs
- `Traceback` `[]string` json:`traceback,omitempty` — Error traceback for error outputs
- `Data` `map[string]interface{}` json:`data,omitempty` — Data for execute_result outputs
- `Metadata` `map[string]interface{}` json:`metadata,omitempty` — Metadata for outputs

##### `NodeJsPackageInfo` — 嵌套（被 `NodeJsRuntimeInfo.RuntimePackages` 引用）
定义于 `nodejs.go`。
- `Name` `string` json:`name` — Package name
- `Version` `string` json:`version` — Package version


---

### 3.7 `mcp` — `client.Mcp`

> MCP：server 列表、工具枚举、调用工具

- 构造：`mcp.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Mcp.WithRawResponse`。
- 方法数：3；涉及类型：21（直接 3 + 嵌套 18）。

**方法（3）**

- `func (c *Client) ListMcpTools(ctx context.Context, serverName string, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListToolsResultModel, error)` — List all available tools from the specified MCP server Args: server_name: The name of the MCP server as defined in mcp-servers.json Returns: Response containing the list…
- `func (c *Client) ExecuteMcpTool(ctx context.Context, serverName string, toolName string, request map[string]any, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseCallToolResultModel, error)` — Execute a specific tool on the specified MCP server Args: server_name: The name of the MCP server as defined in mcp-servers.json tool_name: The name of the tool to execut…
- `func (c *Client) ListMcpServers(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListStr, error)` — List all configured MCP servers Returns: Response containing the list of configured and filtered MCP servers

**类型**

##### `ResponseCallToolResultModel` — 响应（ExecuteMcpTool）
定义于 `mcp.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*CallToolResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

- `ResponseListStr` — 响应（ListMcpServers）：共享类型（types.go），字段见 §3.0。

##### `ResponseListToolsResultModel` — 响应（ListMcpTools）
定义于 `mcp.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ListToolsResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `CallToolResult` — 嵌套（被 `ResponseCallToolResultModel.Data` 引用）
定义于 `mcp.go`。
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `Content` `[]*ResponseCallToolResultModelDataContentItem` json:`content`
- `StructuredContent` `map[string]interface{}` json:`structuredContent,omitempty`
- `IsError` `*bool` json:`isError,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ListToolsResult` — 嵌套（被 `ResponseListToolsResultModel.Data` 引用）
定义于 `mcp.go`。
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `NextCursor` `*string` json:`nextCursor,omitempty`
- `Tools` `[]*Tool` json:`tools`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ResponseCallToolResultModelDataContentItem` — 嵌套（被 `CallToolResult.Content` 引用）
定义于 `mcp.go`。
- `Type` `string`
- `Text` `*TextContent`
- `Image` `*ImageContent`
- `Audio` `*AudioContent`
- `ResourceLink` `*ResourceLink`
- `Resource` `*EmbeddedResource`

##### `Tool` — 嵌套（被 `ListToolsResult.Tools` 引用）
定义于 `mcp.go`。
- `Name` `string` json:`name`
- `Title` `*string` json:`title,omitempty`
- `Description` `*string` json:`description,omitempty`
- `InputSchema` `map[string]interface{}` json:`inputSchema`
- `OutputSchema` `map[string]interface{}` json:`outputSchema,omitempty`
- `Icons` `[]*Icon` json:`icons,omitempty`
- `Annotations` `*ToolAnnotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `Execution` `*ToolExecution` json:`execution,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ImageContent` — 嵌套（被 `ResponseCallToolResultModelDataContentItem.Image` 引用）
定义于 `mcp.go`。
- `Data` `string` json:`data`
- `MimeType` `string` json:`mimeType`
- `Annotations` `*Annotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `TextContent` — 嵌套（被 `ResponseCallToolResultModelDataContentItem.Text` 引用）
定义于 `mcp.go`。
- `Text` `string` json:`text`
- `Annotations` `*Annotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `AudioContent` — 嵌套（被 `ResponseCallToolResultModelDataContentItem.Audio` 引用）
定义于 `mcp.go`。
- `Data` `string` json:`data`
- `MimeType` `string` json:`mimeType`
- `Annotations` `*Annotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `EmbeddedResource` — 嵌套（被 `ResponseCallToolResultModelDataContentItem.Resource` 引用）
定义于 `mcp.go`。
- `Resource` `*Resource` json:`resource`
- `Annotations` `*Annotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ResourceLink` — 嵌套（被 `ResponseCallToolResultModelDataContentItem.ResourceLink` 引用）
定义于 `mcp.go`。
- `Name` `string` json:`name`
- `Title` `*string` json:`title,omitempty`
- `Uri` `string` json:`uri`
- `Description` `*string` json:`description,omitempty`
- `MimeType` `*string` json:`mimeType,omitempty`
- `Size` `*int` json:`size,omitempty`
- `Icons` `[]*Icon` json:`icons,omitempty`
- `Annotations` `*Annotations` json:`annotations,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ToolExecution` — 嵌套（被 `Tool.Execution` 引用）
定义于 `mcp.go`。
- `TaskSupport` `*ToolExecutionTaskSupport` json:`taskSupport,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `Icon` — 嵌套（被 `Tool.Icons` 引用）
定义于 `mcp.go`。
- `Src` `string` json:`src`
- `MimeType` `*string` json:`mimeType,omitempty`
- `Sizes` `[]string` json:`sizes,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `ToolAnnotations` — 嵌套（被 `Tool.Annotations` 引用）
定义于 `mcp.go`。
- `Title` `*string` json:`title,omitempty`
- `ReadOnlyHint` `*bool` json:`readOnlyHint,omitempty`
- `DestructiveHint` `*bool` json:`destructiveHint,omitempty`
- `IdempotentHint` `*bool` json:`idempotentHint,omitempty`
- `OpenWorldHint` `*bool` json:`openWorldHint,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `Annotations` — 嵌套（被 `ImageContent.Annotations` 引用）
定义于 `mcp.go`。
- `Audience` `[]AnnotationsAudienceItem` json:`audience,omitempty`
- `Priority` `*float64` json:`priority,omitempty`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `Resource` — 嵌套（被 `EmbeddedResource.Resource` 引用）
定义于 `mcp.go`。
- `TextResourceContents` `*TextResourceContents`
- `BlobResourceContents` `*BlobResourceContents`

##### `ToolExecutionTaskSupport` — 嵌套（被 `ToolExecution.TaskSupport` 引用）
`type ToolExecutionTaskSupport string` — 枚举值：`ToolExecutionTaskSupportForbidden` = "forbidden", `ToolExecutionTaskSupportOptional` = "optional", `ToolExecutionTaskSupportRequired` = "required"

##### `AnnotationsAudienceItem` — 嵌套（被 `Annotations.Audience` 引用）
`type AnnotationsAudienceItem string` — 枚举值：`AnnotationsAudienceItemUser` = "user", `AnnotationsAudienceItemAssistant` = "assistant"

##### `TextResourceContents` — 嵌套（被 `Resource.TextResourceContents` 引用）
定义于 `mcp.go`。
- `Uri` `string` json:`uri`
- `MimeType` `*string` json:`mimeType,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `Text` `string` json:`text`
- `ExtraProperties` `map[string]interface{}` json:`-`

##### `BlobResourceContents` — 嵌套（被 `Resource.BlobResourceContents` 引用）
定义于 `mcp.go`。
- `Uri` `string` json:`uri`
- `MimeType` `*string` json:`mimeType,omitempty`
- `Meta` `map[string]interface{}` json:`_meta,omitempty`
- `Blob` `string` json:`blob`
- `ExtraProperties` `map[string]interface{}` json:`-`


---

### 3.8 `browser` — `client.Browser`

> 浏览器实例级：信息、配置、重启、截图、代理 PAC、批量动作序列 execute_action

- 构造：`browser.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Browser.WithRawResponse`。
- 方法数：6；涉及类型：28（直接 6 + 嵌套 22）。

**方法（6）**

- `func (c *Client) GetInfo(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseBrowserInfoResult, error)` — Get information about browser, like cdp url, viewport size, etc.
- `func (c *Client) Screenshot(ctx context.Context, opts ...option.RequestOption) -> (io.Reader, error)` — Take a screenshot of the current display.
- `func (c *Client) ExecuteAction(ctx context.Context, request *sandboxsdkgo.Action, opts ...option.RequestOption) -> (*sandboxsdkgo.ActionResponse, error)` — Execute a validated action on the current display.
- `func (c *Client) SetConfig(ctx context.Context, request *sandboxsdkgo.BrowserConfigRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Execute a validated action on the current display.
- `func (c *Client) Restart(ctx context.Context, request *sandboxsdkgo.RestartRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Restart the browser session.
- `func (c *Client) GetProxyPac(ctx context.Context, opts ...option.RequestOption) -> error` — Return the current PAC file for proxy split-routing (if configured).

**类型**

##### `Action` — 请求（ExecuteAction）
定义于 `browser.go`。
- `ActionType` `string`
- `MoveTo` `*MoveToAction`
- `MoveRel` `*MoveRelAction`
- `Click` `*ClickAction`
- `MouseDown` `*MouseDownAction`
- `MouseUp` `*MouseUpAction`
- `RightClick` `*RightClickAction`
- `DoubleClick` `*DoubleClickAction`
- `DragTo` `*DragToAction`
- `DragRel` `*DragRelAction`
- `Scroll` `*ScrollAction`
- `Typing` `*TypingAction`
- `Press` `*PressAction`
- `KeyDown` `*KeyDownAction`
- `KeyUp` `*KeyUpAction`
- `Hotkey` `*HotkeyAction`
- `Wait` `*WaitAction`

##### `BrowserConfigRequest` — 请求（SetConfig）
定义于 `browser.go`。
- `Resolution` `*Resolution` json:`resolution,omitempty` — The desired screen resolution, allowed values are: 1920x1080, 640x480, 1360x768, 1280x720, 800x600, 1024x768, 1280x800, 1920x1200, 1280x960, 1400x1050, 1680x1050, 1280x1024, 1600x1200.

##### `RestartRequest` — 请求（Restart）
定义于 `browser.go`。
- `Mode` `*Mode` json:`mode,omitempty`
- `UrlBlocklist` `[]string` json:`url_blocklist,omitempty`
- `UrlAllowlist` `[]string` json:`url_allowlist,omitempty`
- `Locale` `*string` json:`locale,omitempty`

##### `ActionResponse` — 响应（ExecuteAction）
定义于 `browser.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty`
- `Data` `*ActionData` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)
- `Status` `*string` json:`status,omitempty`
- `ActionPerformed` `*string` json:`action_performed,omitempty`

- `Response` — 响应（SetConfig, Restart）：共享类型（types.go），字段见 §3.0。

##### `ResponseBrowserInfoResult` — 响应（GetInfo）
定义于 `browser.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*BrowserInfoResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `DragToAction` — 嵌套（被 `Action.DragTo` 引用）
定义于 `browser.go`。
- `X` `float64` json:`x` — Target x-coordinate for drag
- `Y` `float64` json:`y` — Target y-coordinate for drag

##### `PressAction` — 嵌套（被 `Action.Press` 引用）
定义于 `browser.go`。
- `Key` `string` json:`key`

##### `RightClickAction` — 嵌套（被 `Action.RightClick` 引用）
定义于 `browser.go`。
- `X` `*float64` json:`x,omitempty`
- `Y` `*float64` json:`y,omitempty`

##### `HotkeyAction` — 嵌套（被 `Action.Hotkey` 引用）
定义于 `browser.go`。
- `Keys` `[]string` json:`keys`

##### `DoubleClickAction` — 嵌套（被 `Action.DoubleClick` 引用）
定义于 `browser.go`。
- `X` `*float64` json:`x,omitempty`
- `Y` `*float64` json:`y,omitempty`

##### `WaitAction` — 嵌套（被 `Action.Wait` 引用）
定义于 `browser.go`。
- `Duration` `float64` json:`duration` — Duration to wait in seconds

##### `MoveRelAction` — 嵌套（被 `Action.MoveRel` 引用）
定义于 `browser.go`。
- `XOffset` `float64` json:`x_offset` — Relative current position x-axis movement
- `YOffset` `float64` json:`y_offset` — Relative current position y-axis movement

##### `MoveToAction` — 嵌套（被 `Action.MoveTo` 引用）
定义于 `browser.go`。
- `X` `float64` json:`x` — Target x-coordinate
- `Y` `float64` json:`y` — Target y-coordinate

##### `ClickAction` — 嵌套（被 `Action.Click` 引用）
定义于 `browser.go`。
- `X` `*float64` json:`x,omitempty`
- `Y` `*float64` json:`y,omitempty`
- `Button` `*Button` json:`button,omitempty`
- `NumClicks` `*int` json:`num_clicks,omitempty`

##### `MouseUpAction` — 嵌套（被 `Action.MouseUp` 引用）
定义于 `browser.go`。
- `Button` `*Button` json:`button,omitempty`

##### `DragRelAction` — 嵌套（被 `Action.DragRel` 引用）
定义于 `browser.go`。
- `XOffset` `float64` json:`x_offset` — Relative current position x-axis drag movement
- `YOffset` `float64` json:`y_offset` — Relative current position y-axis drag movement

##### `KeyUpAction` — 嵌套（被 `Action.KeyUp` 引用）
定义于 `browser.go`。
- `Key` `string` json:`key`

##### `ScrollAction` — 嵌套（被 `Action.Scroll` 引用）
定义于 `browser.go`。
- `Dx` `*int` json:`dx,omitempty`
- `Dy` `*int` json:`dy,omitempty`

##### `KeyDownAction` — 嵌套（被 `Action.KeyDown` 引用）
定义于 `browser.go`。
- `Key` `string` json:`key`

##### `TypingAction` — 嵌套（被 `Action.Typing` 引用）
定义于 `browser.go`。
- `Text` `string` json:`text`
- `UseClipboard` `*bool` json:`use_clipboard,omitempty` — Use clipboard for better character support (recommended for special/ASCII characters)

##### `MouseDownAction` — 嵌套（被 `Action.MouseDown` 引用）
定义于 `browser.go`。
- `Button` `*Button` json:`button,omitempty`

##### `ActionData` — 嵌套（被 `ActionResponse.Data` 引用）
定义于 `browser.go`。
- `ActionPerformed` `string` json:`action_performed`

##### `Resolution` — 嵌套（被 `BrowserConfigRequest.Resolution` 引用）
定义于 `browser.go`。
- `Width` `int` json:`width` — Screen width in pixels.
- `Height` `int` json:`height` — Screen height in pixels.

##### `BrowserInfoResult` — 嵌套（被 `ResponseBrowserInfoResult.Data` 引用）
定义于 `browser.go`。
- `UserAgent` `string` json:`user_agent` — User agent
- `CdpUrl` `string` json:`cdp_url` — Browser CDP URL
- `VncUrl` `string` json:`vnc_url` — VNC URL
- `CdpUiUrl` `*string` json:`cdp_ui_url,omitempty` — CDP UI URL (browser-ui)
- `Viewport` `*BrowserViewport` json:`viewport` — Display size (from xrandr / env vars)
- `PageViewport` `*BrowserViewport` json:`page_viewport,omitempty` — Actual Chrome page viewport (window.innerWidth/Height via CDP).

##### `Mode` — 嵌套（被 `RestartRequest.Mode` 引用）
`type Mode string` — 枚举值：`ModeSoft` = "soft", `ModeHard` = "hard"

##### `Button` — 嵌套（被 `ClickAction.Button` 引用）
`type Button string` — 枚举值：`ButtonLeft` = "left", `ButtonRight` = "right", `ButtonMiddle` = "middle"

##### `BrowserViewport` — 嵌套（被 `BrowserInfoResult.Viewport` 引用）
定义于 `browser.go`。
- `Width` `int` json:`width` — Viewport width
- `Height` `int` json:`height` — Viewport height


---

### 3.9 `browserpage` — `client.BrowserPage`

> 页面级（方法最多）：导航/前进后退、点击/输入/按键/悬停、表单、滚动、截图、HTML/文本/Markdown、元素、控制台、录制、等待、JS 评估

- 构造：`browserpage.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserPage.WithRawResponse`。
- 方法数：29；涉及类型：29（直接 26 + 嵌套 3）。

**方法（29）**

- `func (c *Client) Navigate(ctx context.Context, request *sandboxsdkgo.NavigateRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseDict, error)` — Navigate the current page to a URL.
- `func (c *Client) Back(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Go back in browser history.
- `func (c *Client) Forward(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Go forward in browser history.
- `func (c *Client) Reload(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Reload the current page.
- `func (c *Client) Click(ctx context.Context, request *sandboxsdkgo.ClickRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Click an element by selector, index, or coordinates.
- `func (c *Client) Fill(ctx context.Context, request *sandboxsdkgo.FillRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Fill an input field.
- `func (c *Client) TypeText(ctx context.Context, request *sandboxsdkgo.TypeTextRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Type text with optional delay between keystrokes.
- `func (c *Client) PressKey(ctx context.Context, request *sandboxsdkgo.KeyRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Press a single key.
- `func (c *Client) HotKey(ctx context.Context, request *sandboxsdkgo.HotKeyRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Press a key combination.
- `func (c *Client) Hover(ctx context.Context, request *sandboxsdkgo.HoverRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Hover over an element by selector or coordinates.
- `func (c *Client) SelectOption(ctx context.Context, request *sandboxsdkgo.SelectOptionRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Select an option in a dropdown.
- `func (c *Client) Check(ctx context.Context, request *sandboxsdkgo.CheckRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Check a checkbox.
- `func (c *Client) Uncheck(ctx context.Context, request *sandboxsdkgo.CheckRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Uncheck a checkbox.
- `func (c *Client) UploadFile(ctx context.Context, request *sandboxsdkgo.UploadFileRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Upload files to a file input element.
- `func (c *Client) FillForm(ctx context.Context, request *sandboxsdkgo.FormFillRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Batch fill multiple form fields.
- `func (c *Client) Scroll(ctx context.Context, request *sandboxsdkgo.ScrollRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Scroll the page in a direction.
- `func (c *Client) ScrollTo(ctx context.Context, request *sandboxsdkgo.ScrollToRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Scroll to an absolute position.
- `func (c *Client) ScrollToElement(ctx context.Context, request *sandboxsdkgo.ScrollToElementRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Scroll an element into view.
- `func (c *Client) Screenshot(ctx context.Context, request *sandboxsdkgo.BrowserPageScreenshotRequest, opts ...option.RequestOption) -> (io.Reader, error)` — Capture a page screenshot.
- `func (c *Client) Record(ctx context.Context, request *sandboxsdkgo.RecordRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseDict, error)` — Record page screencast (once/start/pause/resume/stop/status).
- `func (c *Client) GetHtml(ctx context.Context, request *sandboxsdkgo.BrowserPageGetHtmlRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseStr, error)` — Get the page HTML content.
- `func (c *Client) GetText(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseStr, error)` — Get all visible text from the page.
- `func (c *Client) GetMarkdown(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseDict, error)` — Get the page content as Markdown using Readability and Turndown.
- `func (c *Client) GetElements(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — Get all interactive elements on the page.
- `func (c *Client) GetConsole(ctx context.Context, request *sandboxsdkgo.BrowserPageGetConsoleRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — Get captured browser console log messages.
- `func (c *Client) ExportConsole(ctx context.Context, request *sandboxsdkgo.ExportConsoleLogsRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Export console logs to a JSON file.
- `func (c *Client) Evaluate(ctx context.Context, request *sandboxsdkgo.EvaluateRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Execute JavaScript and return the result.
- `func (c *Client) FindText(ctx context.Context, request *sandboxsdkgo.FindTextRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — Find text occurrences on the page.
- `func (c *Client) Wait(ctx context.Context, request *sandboxsdkgo.WaitRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Unified wait: selector, load, url, network_idle, download, function, response, request, or timeout.

**类型**

##### `BrowserPageGetConsoleRequest` — 请求（GetConsole）
定义于 `browser_page.go`。
- `Clear` `*bool` json:`-`

##### `BrowserPageGetHtmlRequest` — 请求（GetHtml）
定义于 `browser_page.go`。
- `Outer` `*bool` json:`-`

##### `BrowserPageScreenshotRequest` — 请求（Screenshot）
定义于 `browser_page.go`。
- `FullPage` `*bool` json:`-`
- `Format` `*string` json:`-`
- `Quality` `*int` json:`-`

##### `CheckRequest` — 请求（Check, Uncheck）
定义于 `browser_page.go`。
- `Selector` `string` json:`selector`

##### `ClickRequest` — 请求（Click）
定义于 `browser_page.go`。
- `Selector` `*string` json:`selector,omitempty`
- `Index` `*int` json:`index,omitempty`
- `X` `*float64` json:`x,omitempty`
- `Y` `*float64` json:`y,omitempty`
- `Button` `*string` json:`button,omitempty`
- `ClickCount` `*int` json:`click_count,omitempty`

##### `EvaluateRequest` — 请求（Evaluate）
定义于 `browser_page.go`。
- `Expression` `string` json:`expression`

##### `ExportConsoleLogsRequest` — 请求（ExportConsole）
定义于 `browser_page.go`。
- `SavePath` `string` json:`save_path`
- `Clear` `*bool` json:`clear,omitempty`

##### `FillRequest` — 请求（Fill）
定义于 `browser_page.go`。
- `Selector` `*string` json:`selector,omitempty`
- `Index` `*int` json:`index,omitempty`
- `Text` `string` json:`text`

##### `FindTextRequest` — 请求（FindText）
定义于 `browser_page.go`。
- `Keyword` `string` json:`keyword`

##### `FormFillRequest` — 请求（FillForm）
定义于 `browser_page.go`。
- `Items` `[]map[string]interface{}` json:`items,omitempty`

##### `HotKeyRequest` — 请求（HotKey）
定义于 `browser_page.go`。
- `Keys` `[]string` json:`keys,omitempty`

##### `HoverRequest` — 请求（Hover）
定义于 `browser_page.go`。
- `Selector` `*string` json:`selector,omitempty`
- `X` `*float64` json:`x,omitempty`
- `Y` `*float64` json:`y,omitempty`

##### `KeyRequest` — 请求（PressKey）
定义于 `browser_page.go`。
- `Key` `string` json:`key`

##### `NavigateRequest` — 请求（Navigate）
定义于 `browser_page.go`。
- `Url` `string` json:`url`
- `WaitUntil` `*NavigateRequestWaitUntil` json:`wait_until,omitempty`
- `Timeout` `*float64` json:`timeout,omitempty`

##### `RecordRequest` — 请求（Record）
定义于 `browser_page.go`。
- `Action` `*RecordRequestAction` json:`action,omitempty`
- `SavePath` `*string` json:`save_path,omitempty`
- `Duration` `*float64` json:`duration,omitempty`
- `Fps` `*int` json:`fps,omitempty`
- `Quality` `*int` json:`quality,omitempty`

##### `ScrollRequest` — 请求（Scroll）
定义于 `browser_page.go`。
- `Direction` `*string` json:`direction,omitempty`
- `Amount` `*int` json:`amount,omitempty`

##### `ScrollToElementRequest` — 请求（ScrollToElement）
定义于 `browser_page.go`。
- `Selector` `string` json:`selector`

##### `ScrollToRequest` — 请求（ScrollTo）
定义于 `browser_page.go`。
- `X` `*int` json:`x,omitempty`
- `Y` `*int` json:`y,omitempty`

##### `SelectOptionRequest` — 请求（SelectOption）
定义于 `browser_page.go`。
- `Selector` `string` json:`selector`
- `Value` `*string` json:`value,omitempty`
- `Label` `*string` json:`label,omitempty`
- `Index` `*int` json:`index,omitempty`

##### `TypeTextRequest` — 请求（TypeText）
定义于 `browser_page.go`。
- `Text` `string` json:`text`
- `Delay` `*float64` json:`delay,omitempty`

##### `UploadFileRequest` — 请求（UploadFile）
定义于 `browser_page.go`。
- `Selector` `string` json:`selector`
- `Files` `[]string` json:`files,omitempty`

##### `WaitRequest` — 请求（Wait）
定义于 `browser_page.go`。
- `Type` `Type` json:`type`
- `Selector` `*string` json:`selector,omitempty`
- `State` `*string` json:`state,omitempty`
- `Url` `*string` json:`url,omitempty`
- `SavePath` `*string` json:`save_path,omitempty`
- `Timeout` `*float64` json:`timeout,omitempty`
- `Expression` `*string` json:`expression,omitempty`
- `Polling` `*float64` json:`polling,omitempty`
- `UrlPattern` `*string` json:`url_pattern,omitempty`

- `Response` — 响应（Back, Forward, Reload, Click, Fill, TypeText, PressKey, HotKey, Hover, SelectOption, Check, Uncheck, UploadFile, FillForm, Scroll, ScrollTo, ScrollToElement, ExportConsole, Evaluate, Wait）：共享类型（types.go），字段见 §3.0。

- `ResponseDict` — 响应（Navigate, Record, GetMarkdown）：共享类型（types.go），字段见 §3.0。

- `ResponseList` — 响应（GetElements, GetConsole, FindText）：共享类型（types.go），字段见 §3.0。

- `ResponseStr` — 响应（GetHtml, GetText）：共享类型（types.go），字段见 §3.0。

**嵌套类型**（响应/请求内引用）

##### `NavigateRequestWaitUntil` — 嵌套（被 `NavigateRequest.WaitUntil` 引用）
`type NavigateRequestWaitUntil string` — 枚举值：`NavigateRequestWaitUntilLoad` = "load", `NavigateRequestWaitUntilDomcontentloaded` = "domcontentloaded", `NavigateRequestWaitUntilNetworkidle` = "networkidle", `NavigateRequestWaitUntilCommit` = "commit"

##### `RecordRequestAction` — 嵌套（被 `RecordRequest.Action` 引用）
`type RecordRequestAction string` — 枚举值：`RecordRequestActionOnce` = "once", `RecordRequestActionStart` = "start", `RecordRequestActionPause` = "pause", `RecordRequestActionResume` = "resume", `RecordRequestActionStop` = "stop", `RecordRequestActionStatus` = "status"

##### `Type` — 嵌套（被 `WaitRequest.Type` 引用）
`type Type string` — 枚举值：`TypeSelector` = "selector", `TypeLoad` = "load", `TypeUrl` = "url", `TypeNetworkIdle` = "network_idle", `TypeDownload` = "download", `TypeFunction` = "function", `TypeResponse` = "response", `TypeRequest` = "request", `TypeTimeout` = "timeout"


---

### 3.10 `browsertabs` — `client.BrowserTabs`

> 标签页：列表、创建（新开页）、关闭、激活

- 构造：`browsertabs.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserTabs.WithRawResponse`。
- 方法数：4；涉及类型：3（直接 3 + 嵌套 0）。

**方法（4）**

- `func (c *Client) List(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — List all open browser tabs.
- `func (c *Client) Create(ctx context.Context, request *sandboxsdkgo.CreatePageRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Create a new browser tab.
- `func (c *Client) Close(ctx context.Context, index int, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Close a browser tab by index.
- `func (c *Client) Activate(ctx context.Context, index int, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Activate (bring to front) a browser tab by index.

**类型**

##### `CreatePageRequest` — 请求（Create）
定义于 `browser_tabs.go`。
- `Url` `*string` json:`url,omitempty`

- `Response` — 响应（Create, Close, Activate）：共享类型（types.go），字段见 §3.0。

- `ResponseList` — 响应（List）：共享类型（types.go），字段见 §3.0。


---

### 3.11 `browsercookies` — `client.BrowserCookies`

> Cookie：读取、设置、清空

- 构造：`browsercookies.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserCookies.WithRawResponse`。
- 方法数：3；涉及类型：4（直接 4 + 嵌套 0）。

**方法（3）**

- `func (c *Client) GetCookies(ctx context.Context, request *sandboxsdkgo.BrowserCookiesGetCookiesRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — Get browser cookies, optionally filtered by URLs (comma-separated).
- `func (c *Client) SetCookies(ctx context.Context, request *sandboxsdkgo.CookieSetRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Set browser cookies.
- `func (c *Client) ClearCookies(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Clear all browser cookies.

**类型**

##### `BrowserCookiesGetCookiesRequest` — 请求（GetCookies）
定义于 `browser_cookies.go`。
- `Urls` `*string` json:`-`

##### `CookieSetRequest` — 请求（SetCookies）
定义于 `browser_cookies.go`。
- `Cookies` `[]map[string]interface{}` json:`cookies,omitempty`

- `Response` — 响应（SetCookies, ClearCookies）：共享类型（types.go），字段见 §3.0。

- `ResponseList` — 响应（GetCookies）：共享类型（types.go），字段见 §3.0。


---

### 3.12 `browserstate` — `client.BrowserState`

> 浏览器状态（storage state）：保存 / 加载

- 构造：`browserstate.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserState.WithRawResponse`。
- 方法数：2；涉及类型：3（直接 3 + 嵌套 0）。

**方法（2）**

- `func (c *Client) Save(ctx context.Context, request *sandboxsdkgo.StateSaveRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Save browser state (cookies, localStorage, etc.) to a file.
- `func (c *Client) Load(ctx context.Context, request *sandboxsdkgo.StateLoadRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Load browser state from a previously saved file.

**类型**

##### `StateLoadRequest` — 请求（Load）
定义于 `browser_state.go`。
- `Path` `string` json:`path`

##### `StateSaveRequest` — 请求（Save）
定义于 `browser_state.go`。
- `Path` `string` json:`path`

- `Response` — 响应（Save, Load）：共享类型（types.go），字段见 §3.0。


---

### 3.13 `browsernetwork` — `client.BrowserNetwork`

> 网络：请求头（全局/按域名）、路由拦截增删、请求记录、HAR 导出

- 构造：`browsernetwork.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserNetwork.WithRawResponse`。
- 方法数：6；涉及类型：9（直接 8 + 嵌套 1）。

**方法（6）**

- `func (c *Client) SetHeaders(ctx context.Context, request *sandboxsdkgo.HeadersRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Set extra HTTP headers for all subsequent requests.
- `func (c *Client) SetScopedHeaders(ctx context.Context, request *sandboxsdkgo.ScopedHeadersRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Set HTTP headers scoped to a specific origin.
- `func (c *Client) AddRoute(ctx context.Context, request *sandboxsdkgo.NetworkRouteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Add a network route to mock or abort matching requests.
- `func (c *Client) RemoveRoute(ctx context.Context, request *sandboxsdkgo.NetworkRouteRemoveRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Remove a previously added network route.
- `func (c *Client) GetRequests(ctx context.Context, request *sandboxsdkgo.BrowserNetworkGetRequestsRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseList, error)` — Get tracked network requests.
- `func (c *Client) ExportHar(ctx context.Context, request *sandboxsdkgo.ExportHarRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Export tracked network requests as a HAR file.

**类型**

##### `BrowserNetworkGetRequestsRequest` — 请求（GetRequests）
定义于 `browser_network.go`。
- `Filter` `*string` json:`-`
- `Limit` `*int` json:`-`

##### `ExportHarRequest` — 请求（ExportHar）
定义于 `browser_network.go`。
- `SavePath` `string` json:`save_path`

##### `HeadersRequest` — 请求（SetHeaders）
定义于 `browser_network.go`。
- `Headers` `map[string]string` json:`headers,omitempty`

##### `NetworkRouteRemoveRequest` — 请求（RemoveRoute）
定义于 `browser_network.go`。
- `UrlPattern` `string` json:`url_pattern`

##### `NetworkRouteRequest` — 请求（AddRoute）
定义于 `browser_network.go`。
- `UrlPattern` `string` json:`url_pattern`
- `Response` `*RouteResponseModel` json:`response,omitempty`
- `Abort` `*bool` json:`abort,omitempty`

##### `ScopedHeadersRequest` — 请求（SetScopedHeaders）
定义于 `browser_network.go`。
- `Origin` `string` json:`origin`
- `Headers` `map[string]string` json:`headers,omitempty`

- `Response` — 响应（SetHeaders, SetScopedHeaders, AddRoute, RemoveRoute, ExportHar）：共享类型（types.go），字段见 §3.0。

- `ResponseList` — 响应（GetRequests）：共享类型（types.go），字段见 §3.0。

**嵌套类型**（响应/请求内引用）

##### `RouteResponseModel` — 嵌套（被 `NetworkRouteRequest.Response` 引用）
定义于 `browser_network.go`。
- `Status` `*int` json:`status,omitempty`
- `Headers` `map[string]string` json:`headers,omitempty`
- `Body` `*string` json:`body,omitempty`
- `ContentType` `*string` json:`content_type,omitempty`


---

### 3.14 `browsercaptcha` — `client.BrowserCaptcha`

> 验证码：检测 / 等待识别

- 构造：`browsercaptcha.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.BrowserCaptcha.WithRawResponse`。
- 方法数：2；涉及类型：4（直接 3 + 嵌套 1）。

**方法（2）**

- `func (c *Client) Detect(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Detect CAPTCHA on the current page.
- `func (c *Client) Wait(ctx context.Context, request *sandboxsdkgo.CaptchaWaitRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseCaptchaWaitResult, error)` — Wait for CAPTCHA to be resolved.

**类型**

##### `CaptchaWaitRequest` — 请求（Wait）
定义于 `browser_captcha.go`。
- `Timeout` `*float64` json:`timeout,omitempty`
- `PollInterval` `*float64` json:`poll_interval,omitempty`

- `Response` — 响应（Detect）：共享类型（types.go），字段见 §3.0。

##### `ResponseCaptchaWaitResult` — 响应（Wait）
定义于 `browser_captcha.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*CaptchaWaitResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `CaptchaWaitResult` — 嵌套（被 `ResponseCaptchaWaitResult.Data` 引用）
定义于 `browser_captcha.go`。
- `Resolved` `bool` json:`resolved`


---

### 3.15 `code` — `client.Code`

> 代码执行：语言信息查询 + 执行代码

- 构造：`code.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Code.WithRawResponse`。
- 方法数：2；涉及类型：7（直接 3 + 嵌套 4）。

**方法（2）**

- `func (c *Client) ExecuteCode(ctx context.Context, request *sandboxsdkgo.CodeExecuteRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseCodeExecuteResponse, error)` — Run code through the unified runtime, dispatching to Python, Node.js, or future language executors
- `func (c *Client) GetInfo(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseCodeInfoResponse, error)` — Return metadata about supported code runtimes Note: Version info is cached at service level (first call only runs subprocess).

**类型**

##### `CodeExecuteRequest` — 请求（ExecuteCode）
定义于 `code.go`。
- `Language` `Language` json:`language` — Target runtime language
- `Code` `string` json:`code` — Source code to execute
- `Timeout` `*int` json:`timeout,omitempty` — Execution timeout in seconds
- `Cwd` `*string` json:`cwd,omitempty` — Current working directory for code execution
- `Stateful` `*bool` json:`stateful,omitempty` — Enable stateful execution using Jupyter kernel.
- `SessionId` `*string` json:`session_id,omitempty` — Session ID for stateful execution.

##### `ResponseCodeExecuteResponse` — 响应（ExecuteCode）
定义于 `code.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*CodeExecuteResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseCodeInfoResponse` — 响应（GetInfo）
定义于 `code.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*CodeInfoResponse` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `Language` — 嵌套（被 `CodeExecuteRequest.Language` 引用）
> Supported programming languages for code execution
`type Language string` — 枚举值：`LanguagePython` = "python", `LanguageJavascript` = "javascript"

##### `CodeExecuteResponse` — 嵌套（被 `ResponseCodeExecuteResponse.Data` 引用）
定义于 `code.go`。
- `Language` `Language` json:`language` — Runtime language that executed the code
- `Status` `string` json:`status` — Execution status indicator
- `Outputs` `[]map[string]interface{}` json:`outputs,omitempty` — Structured execution outputs
- `Code` `string` json:`code` — Echo of executed code
- `Stdout` `*string` json:`stdout,omitempty` — Captured standard output stream
- `Stderr` `*string` json:`stderr,omitempty` — Captured standard error stream
- `ExitCode` `*int` json:`exit_code,omitempty` — Process exit code when applicable
- `Traceback` `[]string` json:`traceback,omitempty` — Captured error traceback lines when available
- `SessionId` `*string` json:`session_id,omitempty` — Session ID for stateful execution (only present when stateful=True)

##### `CodeInfoResponse` — 嵌套（被 `ResponseCodeInfoResponse.Data` 引用）
定义于 `code.go`。
- `Languages` `[]*CodeLanguageInfo` json:`languages` — List of supported languages and metadata

##### `CodeLanguageInfo` — 嵌套（被 `CodeInfoResponse.Languages` 引用）
定义于 `code.go`。
- `Language` `Language` json:`language` — Supported language identifier
- `Description` `string` json:`description` — Human readable runtime description
- `RuntimeVersion` `*string` json:`runtime_version,omitempty` — Primary runtime version identifier
- `DefaultTimeout` `*int` json:`default_timeout,omitempty` — Default timeout in seconds
- `MaxTimeout` `*int` json:`max_timeout,omitempty` — Maximum allowed timeout in seconds
- `Details` `map[string]interface{}` json:`details,omitempty` — Additional runtime specific metadata


---

### 3.16 `util` — `client.Util`

> 工具：URI 转 Markdown

- 构造：`util.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Util.WithRawResponse`。
- 方法数：1；涉及类型：2（直接 2 + 嵌套 0）。

**方法（1）**

- `func (c *Client) ConvertToMarkdown(ctx context.Context, request *sandboxsdkgo.UtilConvertToMarkdownRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Convert a given URI to Markdown format

**类型**

##### `UtilConvertToMarkdownRequest` — 请求（ConvertToMarkdown）
定义于 `util.go`。
- `Uri` `string` json:`uri` — The URI of the resource to convert

- `Response` — 响应（ConvertToMarkdown）：共享类型（types.go），字段见 §3.0。


---

### 3.17 `skills` — `client.Skills`

> 技能包：注册、元数据列表、读取内容、删除、清空

- 构造：`skills.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Skills.WithRawResponse`。
- 方法数：5；涉及类型：12（直接 7 + 嵌套 5）。

**方法（5）**

- `func (c *Client) RegisterSkills(ctx context.Context, request *sandboxsdkgo.BodyRegisterSkills, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseSkillRegistrationResult, error)`
- `func (c *Client) ListMetadata(ctx context.Context, request *sandboxsdkgo.SkillsListMetadataRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseSkillMetadataCollection, error)`
- `func (c *Client) ClearSkills(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseDict, error)`
- `func (c *Client) DeleteSkill(ctx context.Context, name string, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseSkillMetadata, error)`
- `func (c *Client) GetContent(ctx context.Context, name string, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseSkillContentResult, error)`

**类型**

##### `BodyRegisterSkills` — 请求（RegisterSkills）
定义于 `skills.go`。
- `File` `io.Reader` json:`-`
- `Path` `*string` json:`path,omitempty`
- `Name` `*string` json:`name,omitempty`

##### `SkillsListMetadataRequest` — 请求（ListMetadata）
定义于 `skills.go`。
- `Names` `*string` json:`-`

- `ResponseDict` — 响应（ClearSkills）：共享类型（types.go），字段见 §3.0。

##### `ResponseSkillContentResult` — 响应（GetContent）
定义于 `skills.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*SkillContentResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseSkillMetadata` — 响应（DeleteSkill）
定义于 `skills.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*SkillMetadata` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseSkillMetadataCollection` — 响应（ListMetadata）
定义于 `skills.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*SkillMetadataCollection` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseSkillRegistrationResult` — 响应（RegisterSkills）
定义于 `skills.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*SkillRegistrationResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `SkillContentResult` — 嵌套（被 `ResponseSkillContentResult.Data` 引用）
定义于 `skills.go`。
- `Name` `string` json:`name` — Skill name
- `Path` `string` json:`path` — Absolute path to the skill directory
- `Content` `string` json:`content` — Skill content excluding front matter

##### `SkillMetadata` — 嵌套（被 `ResponseSkillMetadata.Data` 引用）
定义于 `skills.go`。
- `Name` `string` json:`name` — Skill name
- `Path` `string` json:`path` — Absolute path to the skill directory
- `Metadata` `map[string]interface{}` json:`metadata,omitempty` — Metadata parsed from SKILL.md front matter
- `DependencyCommands` `[]*DependencyCommandResult` json:`dependency_commands,omitempty` — Dependency commands for the skill

##### `SkillMetadataCollection` — 嵌套（被 `ResponseSkillMetadataCollection.Data` 引用）
定义于 `skills.go`。
- `Skills` `[]*SkillMetadata` json:`skills,omitempty` — Collection of skill metadata entries

##### `SkillRegistrationResult` — 嵌套（被 `ResponseSkillRegistrationResult.Data` 引用）
定义于 `skills.go`。
- `Count` `int` json:`count` — Number of registered skills
- `Registered` `[]*SkillMetadata` json:`registered,omitempty` — Registered skills and metadata

##### `DependencyCommandResult` — 嵌套（被 `SkillMetadata.DependencyCommands` 引用）
定义于 `skills.go`。
- `Command` `[]string` json:`command` — Executed dependency command
- `Success` `bool` json:`success` — Whether the command succeeded
- `Stdout` `*string` json:`stdout,omitempty` — Standard output from command
- `Stderr` `*string` json:`stderr,omitempty` — Standard error from command


---

### 3.18 `proxy` — `client.Proxy`

> 代理：映射与排除项增删查、上游设置、健康检查、诊断

- 构造：`proxy.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Proxy.WithRawResponse`。
- 方法数：11；涉及类型：16（直接 12 + 嵌套 4）。

**方法（11）**

- `func (c *Client) ListMappings(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListProxyMappingRoute, error)` — List all proxy domain-to-port mappings.
- `func (c *Client) AddMapping(ctx context.Context, request *sandboxsdkgo.ProxyMappingAddRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseProxyMappingRoute, error)` — Add or update a proxy mapping.
- `func (c *Client) RemoveMapping(ctx context.Context, source string, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Remove a proxy mapping by source pattern.
- `func (c *Client) ListExcludes(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseListStr, error)` — List all proxy bypass/exclude patterns.
- `func (c *Client) AddExclude(ctx context.Context, request *sandboxsdkgo.ProxyBypassRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Add a bypass/exclude pattern.
- `func (c *Client) RemoveExclude(ctx context.Context, request *sandboxsdkgo.ProxyBypassRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Remove a bypass/exclude pattern.
- `func (c *Client) Diagnose(ctx context.Context, request *sandboxsdkgo.ProxyDiagnoseRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseProxyDiagnoseResult, error)` — Diagnose how a URL would be routed through the proxy.
- `func (c *Client) Health(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseProxyHealthCheck, error)` — Check proxy subsystem health: GOST alive, nginx alive, config consistency.
- `func (c *Client) GetUpstream(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseUnionProxyUpstreamInfoNoneType, error)` — Get the current upstream proxy configuration.
- `func (c *Client) SetUpstream(ctx context.Context, request *sandboxsdkgo.ProxyUpstreamUpdateRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseProxyUpstreamInfo, error)` — Set or update the upstream proxy.
- `func (c *Client) RemoveUpstream(ctx context.Context, opts ...option.RequestOption) -> (*sandboxsdkgo.Response, error)` — Remove upstream proxy (switch to direct mode).

**类型**

##### `ProxyBypassRequest` — 请求（AddExclude, RemoveExclude）
定义于 `proxy.go`。
- `Pattern` `string` json:`pattern` — Bypass pattern: domain (*.example.com, .example.com) or CIDR (10.0.0.0/8)

##### `ProxyDiagnoseRequest` — 请求（Diagnose）
定义于 `proxy.go`。
- `Url` `string` json:`-` — URL to diagnose routing for

##### `ProxyMappingAddRequest` — 请求（AddMapping）
定义于 `proxy.go`。
- `Source` `string` json:`source` — Source pattern: [protocol://]host[:port][/path], supports wildcard *
- `Target` `string` json:`target` — Target address: [host:]port[/path].

##### `ProxyUpstreamUpdateRequest` — 请求（SetUpstream）
定义于 `proxy.go`。
- `Server` `string` json:`server` — Upstream proxy server.
- `AuthCmd` `*string` json:`auth_cmd,omitempty` — Optional shell command to obtain proxy credentials.

- `Response` — 响应（RemoveMapping, AddExclude, RemoveExclude, RemoveUpstream）：共享类型（types.go），字段见 §3.0。

##### `ResponseListProxyMappingRoute` — 响应（ListMappings）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `[]*ProxyMappingRoute` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

- `ResponseListStr` — 响应（ListExcludes）：共享类型（types.go），字段见 §3.0。

##### `ResponseProxyDiagnoseResult` — 响应（Diagnose）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ProxyDiagnoseResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseProxyHealthCheck` — 响应（Health）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ProxyHealthCheck` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseProxyMappingRoute` — 响应（AddMapping）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ProxyMappingRoute` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseProxyUpstreamInfo` — 响应（SetUpstream）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ProxyUpstreamInfo` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

##### `ResponseUnionProxyUpstreamInfoNoneType` — 响应（GetUpstream）
定义于 `proxy.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*ProxyUpstreamInfo` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `ProxyMappingRoute` — 嵌套（被 `ResponseListProxyMappingRoute.Data` 引用）
定义于 `proxy.go`。
- `Source` `string` json:`source` — Source pattern: [protocol://]host[:port][/path], supports wildcard * in host
- `Target` `string` json:`target` — Target address: [host:]port[/path].
- `SourceHost` `string` json:`source_host` — Extracted host from source (used for GOST hosts)
- `SourcePath` `*string` json:`source_path,omitempty` — Extracted path from source (used for nginx location)
- `InternalPort` `int` json:`internal_port` — Internal nginx listen port for this domain group

##### `ProxyDiagnoseResult` — 嵌套（被 `ResponseProxyDiagnoseResult.Data` 引用）
定义于 `proxy.go`。
- `Url` `string` json:`url`
- `MatchedMapping` `*ProxyMappingRoute` json:`matched_mapping,omitempty`
- `ResolvedTarget` `*string` json:`resolved_target,omitempty`
- `TargetReachable` `*bool` json:`target_reachable,omitempty`
- `Route` `string` json:`route`

##### `ProxyHealthCheck` — 嵌套（被 `ResponseProxyHealthCheck.Data` 引用）
定义于 `proxy.go`。
- `Healthy` `bool` json:`healthy` — Overall health status
- `GostAlive` `bool` json:`gost_alive` — GOST proxy process is reachable via API
- `NginxAlive` `bool` json:`nginx_alive` — nginx process is running
- `ConfigConsistent` `bool` json:`config_consistent` — Domain sets in proxy-map.json, gost-hosts.txt, and nginx conf are consistent
- `Inconsistencies` `[]string` json:`inconsistencies,omitempty` — List of inconsistency details (empty when config_consistent is true)

##### `ProxyUpstreamInfo` — 嵌套（被 `ResponseProxyUpstreamInfo.Data` 引用）
定义于 `proxy.go`。
- `Addr` `string` json:`addr` — Upstream proxy address (host:port)
- `Username` `*string` json:`username,omitempty` — Proxy auth username (if authenticated)
- `Password` `*string` json:`password,omitempty` — Proxy auth password (if authenticated)


---

### 3.19 `display` — `client.Display`

> 显示录制（Xvfb）：Record

- 构造：`display.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Display.WithRawResponse`。
- 方法数：1；涉及类型：5（直接 2 + 嵌套 3）。

**方法（1）**

- `func (c *Client) Record(ctx context.Context, request *sandboxsdkgo.DisplayRecordRequest, opts ...option.RequestOption) -> (*sandboxsdkgo.ResponseDisplayRecordResult, error)` — Control display screen recording (start/stop/status).

**类型**

##### `DisplayRecordRequest` — 请求（Record）
定义于 `display.go`。
- `Action` `DisplayRecordRequestAction` json:`action` — Recording action: start, stop, or status
- `SavePath` `*string` json:`save_path,omitempty` — Output file path (default: /tmp/recordings/recording_{timestamp}.mp4)
- `Fps` `*int` json:`fps,omitempty` — Frames per second
- `Crf` `*int` json:`crf,omitempty` — H.264 CRF quality (0=lossless, 51=worst)
- `MaxDuration` `*float64` json:`max_duration,omitempty` — Max recording duration in seconds
- `Width` `*int` json:`width,omitempty` — Video width in pixels (auto-detected from X11 if omitted)
- `Height` `*int` json:`height,omitempty` — Video height in pixels (auto-detected from X11 if omitted)

##### `ResponseDisplayRecordResult` — 响应（Record）
定义于 `display.go`。
- `Success` `*bool` json:`success,omitempty` — Whether the operation was successful
- `Message` `*string` json:`message,omitempty` — Operation result message
- `Data` `*DisplayRecordResult` json:`data,omitempty` — Data returned from the operation
- `Hint` `*string` json:`hint,omitempty` — Context hint for AI agents (e.g. tab changes)

**嵌套类型**（响应/请求内引用）

##### `DisplayRecordRequestAction` — 嵌套（被 `DisplayRecordRequest.Action` 引用）
> Recording action: start, stop, or status
`type DisplayRecordRequestAction string` — 枚举值：`DisplayRecordRequestActionStart` = "start", `DisplayRecordRequestActionStop` = "stop", `DisplayRecordRequestActionStatus` = "status"

##### `DisplayRecordResult` — 嵌套（被 `ResponseDisplayRecordResult.Data` 引用）
定义于 `display.go`。
- `Status` `Status` json:`status`
- `SavePath` `*string` json:`save_path,omitempty`
- `Duration` `*float64` json:`duration,omitempty`
- `FileSizeBytes` `*int` json:`file_size_bytes,omitempty`

##### `Status` — 嵌套（被 `DisplayRecordResult.Status` 引用）
`type Status string` — 枚举值：`StatusIdle` = "idle", `StatusRecording` = "recording", `StatusStopped` = "stopped"


---

### 3.20 `auth` — `client.Auth`

> 认证：创建 ticket、校验请求（供 nginx auth_request 场景）

- 构造：`auth.NewClient(options *core.RequestOptions) *Client`（由 `client.NewClient()` 自动完成）；Raw 通道：`c.Auth.WithRawResponse`。
- 方法数：2；涉及类型：0（直接 0 + 嵌套 0）。

**方法（2）**

- `func (c *Client) CreateTicket(ctx context.Context, opts ...option.RequestOption) -> (map[string]any, error)` — Create and return a short-lived authentication ticket.
- `func (c *Client) Authenticate(ctx context.Context, opts ...option.RequestOption) -> (map[string]string, error)` — Authenticate a request using ticket or JWT.

**类型**


---

## 附录 A. 命名空间 × 三 SDK 对照表

三个 SDK 的顶层命名空间集合**完全相同（各 20 个）**，仅命名风格不同（Go 字段 OwnerCase / Python snake_case / JS camelCase）。

| 命名空间 | Go `client.X` | Python `Sandbox.x` | JS `client.x` | 方法数 Go/Py/JS | 备注 |
|---|---|---|---|---|---|
| `sandbox` | `client.Sandbox` | `Sandbox.sandbox` | `client.sandbox` | 6/14/14 | ⚠️ 唯一差集：Go 缺 8 个 observe_*（Py/JS 各多 8 个） |
| `shell` | `client.Shell` | `Sandbox.shell` | `client.shell` | 12/12/12 | — |
| `bash` | `client.Bash` | `Sandbox.bash` | `client.bash` | 7/7/7 | — |
| `file` | `client.File` | `Sandbox.file` | `client.file` | 17/17/17 | — |
| `jupyter` | `client.Jupyter` | `Sandbox.jupyter` | `client.jupyter` | 6/6/6 | — |
| `nodejs` | `client.Nodejs` | `Sandbox.nodejs` | `client.nodejs` | 7/7/7 | — |
| `mcp` | `client.Mcp` | `Sandbox.mcp` | `client.mcp` | 3/3/3 | — |
| `browser` | `client.Browser` | `Sandbox.browser` | `client.browser` | 6/6/6 | — |
| `browserpage` | `client.BrowserPage` | `Sandbox.browser_page` | `client.browserPage` | 29/29/29 | — |
| `browsertabs` | `client.BrowserTabs` | `Sandbox.browser_tabs` | `client.browserTabs` | 4/4/4 | — |
| `browsercookies` | `client.BrowserCookies` | `Sandbox.browser_cookies` | `client.browserCookies` | 3/3/3 | — |
| `browserstate` | `client.BrowserState` | `Sandbox.browser_state` | `client.browserState` | 2/2/2 | — |
| `browsernetwork` | `client.BrowserNetwork` | `Sandbox.browser_network` | `client.browserNetwork` | 6/6/6 | — |
| `browsercaptcha` | `client.BrowserCaptcha` | `Sandbox.browser_captcha` | `client.browserCaptcha` | 2/2/2 | — |
| `code` | `client.Code` | `Sandbox.code` | `client.code` | 2/2/2 | — |
| `util` | `client.Util` | `Sandbox.util` | `client.util` | 1/1/1 | — |
| `skills` | `client.Skills` | `Sandbox.skills` | `client.skills` | 5/5/5 | — |
| `proxy` | `client.Proxy` | `Sandbox.proxy` | `client.proxy` | 11/11/11 | — |
| `display` | `client.Display` | `Sandbox.display` | `client.display` | 1/1/1 | — |
| `auth` | `client.Auth` | `Sandbox.auth` | `client.auth` | 2/2/2 | — |

补充：
- Go 的 `client.Nodejs` 字段拼写是 **Nodejs**（不是 NodeJs）；Python/JS 均为 `nodejs`。
- 三个 SDK 中，命名空间目录名分别：Go `browserpage/`、Python `browser_page/`、JS `browserPage/`（其余同理）。
- Python/JS 的 `providers` 是云厂商（火山引擎）沙箱管理辅助模块，不属于 API 子客户端（见附录 E）。

## 附录 B. 「Python / JS 有、Go 没有」的方法（HTTP 直调补足依据）

### B.1 差集汇总

| 方向 | 数量 | 所在命名空间 | 备注 |
|---|---|---|---|
| 仅 Python / JS 有 | **8** | `sandbox` | 同一组 `observe_*` 遥测方法；Python 与 JS 完全一致 |
| 仅 Go 有 | 0 | — | 不存在 Go 独有方法 |
| 三 SDK 均有 | 132 | 全部 20 个命名空间 | 见附录 C |

### B.2 明细（8 个方法）

| # | 功能 | Python | JS | HTTP | 参数/要点 |
|---|---|---|---|---|---|
| 1 | 查询采样状态 | `observe_status` | `observeStatus` | GET `/v1/sandbox/observe/status` | 无参数 |
| 2 | 实时负载快照 | `observe_live` | `observeLive` | GET `/v1/sandbox/observe/live` | query: `top_rows` |
| 3 | 开始采样会话 | `observe_start` | `observeStart` | POST `/v1/sandbox/observe/start` | body: `mode`(ObserveStartMode), `idempotency_key`, `duration_seconds`, `interval_seconds`, `include_processes`, `include_disk` |
| 4 | 停止采样会话 | `observe_stop` | `observeStop` | POST `/v1/sandbox/observe/stop` | body: `session_id` |
| 5 | 导出报告 | `observe_export` | `observeExport` | POST `/v1/sandbox/observe/export` | body: `idempotency_key`, `session_id`, `reason` |
| 6 | 报告列表 | `observe_reports` | `observeReports` | GET `/v1/sandbox/observe/reports` | 无参数 |
| 7 | 下载报告（流式） | `observe_report_download` | `observeReportDownload` | GET `/v1/sandbox/observe/reports/{report_id}` | path: `report_id` |
| 8 | 删除报告 | `observe_report_delete` | `observeReportDelete` | DELETE `/v1/sandbox/observe/reports/{report_id}` | path: `report_id` |

> 说明：以上路径相对 `baseURL`（与 Go SDK 其它方法同一 base，如 `http://<sandbox-ip>:8080`）。Go SDK 的 `master` / `1.7.5` / `skills` 三个分支均**没有**这些方法——若 CLI 需要该能力，只能用 `net/http` 直调（8 个端点），或升级/等待 Go SDK 重新生成。

## 附录 C. 「三个 SDK 都有」的方法清单（核心方法，逐命名空间）

以下为 Go SDK 的方法名（共 132 个）。Python 名为其 snake_case、JS 名为其 camelCase 一一对应（已程序化校验：无任何不规则转换）。

- **`sandbox`**（6）：`GetContext`、`GetPythonPackages`、`GetNodejsPackages`、`ListHooks`、`RegisterHook`、`RemoveHook`
- **`shell`**（12）：`ExecCommand`、`View`、`WaitForProcess`、`WriteToProcess`、`KillProcess`、`CreateSession`、`UpdateSession`、`GetTerminalUrl`、`GetSessionStats`、`ListSessions`、`CleanupAllSessions`、`CleanupSession`
- **`bash`**（7）：`Exec`、`Output`、`Write`、`Kill`、`Sessions`、`CreateSession`、`CloseSession`
- **`file`**（17）：`ReadFile`、`WriteFile`、`ReplaceInFile`、`SearchInFile`、`FindFiles`、`GrepFiles`、`GlobFiles`、`UploadFile`、`DownloadFile`、`ListPath`、`StrReplaceEditor`、`WatchList`、`WatchCreate`、`WatchEvents`、`WatchPoll`、`WatchWait`、`WatchStop`
- **`jupyter`**（6）：`ExecuteCode`、`GetInfo`、`ListSessions`、`DeleteSessions`、`DeleteSession`、`CreateSession`
- **`nodejs`**（7）：`ExecuteCode`、`GetInfo`、`ListSessions`、`CreateSession`、`GetSession`、`DeleteSession`、`UpdateSession`
- **`mcp`**（3）：`ListMcpTools`、`ExecuteMcpTool`、`ListMcpServers`
- **`browser`**（6）：`GetInfo`、`Screenshot`、`ExecuteAction`、`SetConfig`、`Restart`、`GetProxyPac`
- **`browserpage`**（29）：`Navigate`、`Back`、`Forward`、`Reload`、`Click`、`Fill`、`TypeText`、`PressKey`、`HotKey`、`Hover`、`SelectOption`、`Check`、`Uncheck`、`UploadFile`、`FillForm`、`Scroll`、`ScrollTo`、`ScrollToElement`、`Screenshot`、`Record`、`GetHtml`、`GetText`、`GetMarkdown`、`GetElements`、`GetConsole`、`ExportConsole`、`Evaluate`、`FindText`、`Wait`
- **`browsertabs`**（4）：`List`、`Create`、`Close`、`Activate`
- **`browsercookies`**（3）：`GetCookies`、`SetCookies`、`ClearCookies`
- **`browserstate`**（2）：`Save`、`Load`
- **`browsernetwork`**（6）：`SetHeaders`、`SetScopedHeaders`、`AddRoute`、`RemoveRoute`、`GetRequests`、`ExportHar`
- **`browsercaptcha`**（2）：`Detect`、`Wait`
- **`code`**（2）：`ExecuteCode`、`GetInfo`
- **`util`**（1）：`ConvertToMarkdown`
- **`skills`**（5）：`RegisterSkills`、`ListMetadata`、`ClearSkills`、`DeleteSkill`、`GetContent`
- **`proxy`**（11）：`ListMappings`、`AddMapping`、`RemoveMapping`、`ListExcludes`、`AddExclude`、`RemoveExclude`、`Diagnose`、`Health`、`GetUpstream`、`SetUpstream`、`RemoveUpstream`
- **`display`**（1）：`Record`
- **`auth`**（2）：`CreateTicket`、`Authenticate`

## 附录 D. 联合类型（Visitor 接口）、上传辅助与指针工具

### D.1 Visitor 接口（union 类型）

这些接口不在任何方法的签名里，但为公开 API（`Accept(visitor)` 模式 + 变体结构体都在类型章节中列出）。

##### `ActionVisitor`（`browser.go`）
- `VisitMoveTo(*MoveToAction) error`
- `VisitMoveRel(*MoveRelAction) error`
- `VisitClick(*ClickAction) error`
- `VisitMouseDown(*MouseDownAction) error`
- `VisitMouseUp(*MouseUpAction) error`
- `VisitRightClick(*RightClickAction) error`
- `VisitDoubleClick(*DoubleClickAction) error`
- `VisitDragTo(*DragToAction) error`
- `VisitDragRel(*DragRelAction) error`
- `VisitScroll(*ScrollAction) error`
- `VisitTyping(*TypingAction) error`
- `VisitPress(*PressAction) error`
- `VisitKeyDown(*KeyDownAction) error`
- `VisitKeyUp(*KeyUpAction) error`
- `VisitHotkey(*HotkeyAction) error`
- `VisitWait(*WaitAction) error`

##### `ResourceVisitor`（`mcp.go`）
- `VisitTextResourceContents(*TextResourceContents) error`
- `VisitBlobResourceContents(*BlobResourceContents) error`

##### `ResponseCallToolResultModelDataContentItemVisitor`（`mcp.go`）
- `VisitText(*TextContent) error`
- `VisitImage(*ImageContent) error`
- `VisitAudio(*AudioContent) error`
- `VisitResourceLink(*ResourceLink) error`
- `VisitResource(*EmbeddedResource) error`

##### `ValidationErrorLocItemVisitor`（`types.go`）
- `VisitString(string) error`
- `VisitInteger(int) error`

用法：`func (a *Action) Accept(visitor ActionVisitor) error` —— 对 union 结构体（如 `Action`）调用 `Accept`，把实际变体回调给 visitor。

### D.2 multipart 文件上传辅助（`file_param.go`）

- `func NewFileParam(reader io.Reader, filename string, contentType string, opts ...FileParamOption) *FileParam`
- `type FileParam struct { io.Reader; filename string; contentType string }`，方法：`Name() string`、`ContentType() string`
- `type FileParamOption interface { apply() }`（暂无可选项，预留扩展）
- 说明：文件上传端点（`c.File.UploadFile` 等）直接收 `io.Reader`；需要指定文件名/Content-Type 时用 `NewFileParam`。

### D.3 指针辅助函数（`pointer.go`，包级函数，配合可选字段赋值）

`Bool`、`Byte`、`Complex64`、`Complex128`、`Float32`、`Float64`、`Int`、`Int8`、`Int16`、`Int32`、`Int64`、`Rune`、`String`、`Uint`、`Uint8`、`Uint16`、`Uint32`、`Uint64`、`Uintptr`、`UUID`、`Time`（各返回对应类型的指针）；另有 `MustParseDate(date string) time.Time`、`MustParseDateTime(datetime string) time.Time`。

### D.4 其它共享包

- `core.APIError`、`core.Response[T]{StatusCode, Header, Body}`、`core.HTTPClient` 接口、`core.RequestOptions`/`RequestOption`。
- `internal.ErrorCodes`（422 → `*UnprocessableEntityError` 的映射，SDK 内部使用）。

## 附录 E. 其它备注

1. **Python/JS 的 `providers` 模块**（Go 无）：`agent_sandbox.providers.VolcengineProvider` / JS `providers/volcengine.ts` —— 云侧沙箱管理辅助（create/get/list/set_timeout/delete sandbox 等），对接火山引擎 VEFAAS，不是沙箱内 HTTP API 的一部分；写 Go CLI 若需要可在应用层自行实现 HTTP 签名调用。
2. **主仓库 `sdk/go` 目录**只是个 README，指向独立仓库 `agent-infra/sandbox-sdk-go`（即本文档第 1–3 章对象），因此 Go 版本以独立仓库为准。
3. **枚举**（如 `Language`、`Command`、`Button`、`Mode`、`Status`、`BashCommandStatus` 等 17 个）在每个命名空间章节内以 `枚举值` 形式列出，并可用 `NewXxxFromString(s)` 解析。
4. **数量校验**：子客户端 20 个；Go 方法 132 个（含 `auth` 2 个）；类型 266 个（244 struct + 17 枚举/简单类型 + 5 接口）；字段 904 个。Python/JS 各 140 个方法（=132+8）。
5. 版本活性：Go SDK 最后提交 2026-03-31（`105ef161` = tag `v0.0.5`），Python/JS SDK 最后提交 2026-09-14（`7f1afaf8`）——Go SDK 落后约半年，这是 `observe_*` 缺失的最可能原因。
6. **本目录 `check-parity.sh` 的小坑**：其 `GOREPO` 使用 `.../sandbox-sdk-go/main`，但该仓库**不存在 `main` 分支**（默认 `master`），配合 `curl -f` 会让「Go SDK 子客户端方法」整段静默为空；建议改为 `.../sandbox-sdk-go/master`（或固定 tag `v0.0.5`）。另其 `NS` 列表遗漏了 `auth`、`util`、`proxy`、`display`，可一并补上。
