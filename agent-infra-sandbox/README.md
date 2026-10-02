# agent-infra-sandbox

封装上游 **[agent-infra/sandbox](https://github.com/agent-infra/sandbox)** 的 Go SDK
（`github.com/agent-infra/sandbox-sdk-go`）的命令行工具，产物名 **`sandbox-sdk-go`**。

面向"拿到沙箱之后"的沙箱内操作：命令执行、长任务、持久会话、文件双向传输。

```bash
export SANDBOX_BASE="https://<proxy>/sandbox/<SID>/8080"
go build -o sandbox-sdk-go .
```
