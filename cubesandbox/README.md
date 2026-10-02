# cubesandbox

封装上游 **[TencentCloud/CubeSandbox](https://github.com/TencentCloud/CubeSandbox)** 官方 Go SDK
（`github.com/tencentcloud/CubeSandbox/sdk/go`）的命令行工具，产物名 **`cubesandbox-sdk-go`**。

- `main.go` —— 入口、参数解析、CF 路径改写传输层（`cfTransport`）、命令分发
- `cmd_sandbox.go` —— 沙箱生命周期与执行（new/ls/info/rm/pause/resume/timeout/net/exec/code/pty）
- `cmd_files.go` —— 文件操作（ls-file/stat/cat/put/get/mkdir/rm-file/mv/exists/watch）
- `cmd_misc.go` —— 快照/回滚/克隆、持久卷、模板、健康检查

```bash
export CUBE_API_KEY=<key>
go build -o cubesandbox-sdk-go .
```
