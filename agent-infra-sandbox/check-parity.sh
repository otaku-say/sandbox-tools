#!/usr/bin/env bash
# agent-infra-sandbox/check-parity.sh —— 拉取上游三语言 SDK 的命名空间与方法清单，核对本 CLI 的覆盖度
#
# 用途：上游 agent-infra/sandbox 的 Python/JS SDK 更新后，跑一次本脚本，
#      对照本地已封装的命令，发现缺口就在本目录补实现，再 ./build.sh 或触发 CI。
#
#   ./check-parity.sh > /tmp/ai-api.txt
set -euo pipefail
GOREPO=${GOREPO:-https://raw.githubusercontent.com/agent-infra/sandbox-sdk-go/master}
PYBASE=${PYBASE:-https://raw.githubusercontent.com/agent-infra/sandbox/main/sdk/python/agent_sandbox}
JSBASE=${JSBASE:-https://raw.githubusercontent.com/agent-infra/sandbox/main/sdk/js/src/api/resources}
NS=${NS:-bash shell file code jupyter nodejs browser browser_page browser_tabs browser_state browser_cookies browser_network browser_captcha mcp skills sandbox proxy display util auth}

hr() { printf '\n%s\n' "===== $1 ====="; }

hr "Go SDK 子客户端方法"
for n in $NS; do
  out=$(curl -fsSL --max-time 25 "$GOREPO/$n/client.go" 2>/dev/null | grep -E '^func \(c \*Client\)' | sed 's/ {$//' | head -20 || true)
  [ -n "$out" ] && { echo "--- $n ---"; echo "$out"; }
done

hr "Python SDK 命名空间方法"
for n in $NS; do
  out=$(curl -fsSL --max-time 25 "$PYBASE/$n/client.py" 2>/dev/null | grep -E '^    (async )?def [a-z]' | sed 's/(.*//' | head -20 || true)
  [ -n "$out" ] && { echo "--- $n ---"; echo "$out"; }
done

hr "JS SDK 命名空间方法"
for n in $NS; do
  out=$(curl -fsSL --max-time 25 "$JSBASE/$n/client/Client.ts" 2>/dev/null | grep -oE 'public (async )?[a-zA-Z]+\(' | sed 's/public //;s/(.*//' | sort -u | head -20 || true)
  [ -n "$out" ] && { echo "--- $n ---"; echo "$out"; }
done

hr "本仓库已实现的命令"
grep -hoE '"--?[a-zA-Z][a-zA-Z0-9_]*"' "$(dirname "$0")"/main.go 2>/dev/null | sort -u | tr '\n' ' '
echo
