# sandbox-tools

云端沙箱的命令行客户端（静态二进制，在沙箱内交叉编译）。

## lxcsbx — LXC 沙箱客户端

基于官方 Go SDK（`github.com/agent-infra/sandbox-sdk-go`）编译的静态 CLI。

| 架构 | 资产 |
|---|---|
| Linux x86_64 | `lxcsbx-linux-amd64` |
| Linux aarch64 | `lxcsbx-linux-arm64` |

```sh
BASE="https://github.com/otaku-say/sandbox-tools/releases/latest/download"
curl -fsSL -o /usr/local/bin/lxcsbx "$BASE/lxcsbx-linux-arm64"
chmod +x /usr/local/bin/lxcsbx
```

环境变量：

| 变量 | 说明 | 默认 |
|---|---|---|
| `LXC_SANDBOX_1_KEY` | 沙箱访问密钥（必需） | — |
| `LXC_BASE` | 沙箱端点 | `https://lxc-sandbox-1.<private-host>` |

常用子命令：`exec` / `run` / `sess` / `sessnew` / `sessions` / `view` / `kill` / `job` / `log` / `read` / `write` / `get` / `ps` / `health` / `version`

## 历史

- `v1.0.0` 及更早：命令名为 `ctgo`，环境变量 `CT_SANDBOX_1_KEY` / `CT_BASE`
- `v2.0.0` 起：改名为 `lxcsbx`，环境变量改为 `LXC_SANDBOX_1_KEY` / `LXC_BASE`
