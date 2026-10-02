#!/usr/bin/env bash
# cubesandbox/check-parity.sh —— 拉取上游三语言 SDK 的公开方法清单，用于核对本 CLI 是否漏封能力
#
# 用途：上游 Python/Node SDK 更新后，跑一次本脚本，把输出与本地 cmd_*.go 的命令对照；
#      发现缺口就在本目录补实现，然后 ./build.sh 或触发 CI 单工具构建。
#
#   ./check-parity.sh            # 打印三语言方法清单
#   ./check-parity.sh > /tmp/api.txt
set -euo pipefail
BASE=${BASE:-https://raw.githubusercontent.com/TencentCloud/CubeSandbox/master/sdk}
GO=${GO_FILES:-sandbox client volume snapshot template files pty policy}
PY=${PY_FILES:-sandbox _volume _template _filesystem _pty _policy}
TS=${TS_FILES:-sandbox volume template filesystem pty policy}

hr() { printf '\n%s\n' "===== $1 ====="; }

hr "Go SDK (sdk/go)"
for f in $GO; do
  echo "--- $f.go ---"
  curl -fsSL --max-time 30 "$BASE/go/$f.go" | grep -E '^func (\([a-z]+ \*[A-Za-z]+\) )?[A-Z]' | sed 's/ {$//'
done

hr "Python SDK (sdk/python/cubesandbox)"
for f in $PY; do
  echo "--- $f.py ---"
  curl -fsSL --max-time 30 "$BASE/python/cubesandbox/$f.py" | grep -E '^    def [a-z]' | sed 's/(.*//'
done

hr "Node SDK (sdk/node/src)"
for f in $TS; do
  echo "--- $f.ts ---"
  curl -fsSL --max-time 30 "$BASE/node/src/$f.ts" | grep -E '^  (static )?(async )?[a-zA-Z][a-zA-Z0-9]*[\(<]' | sed 's/[(<].*//' | sort -u
done

hr "本仓库已实现的命令（对照用）"
grep -hoE 'case "[a-z-]+"' "$(dirname "$0")"/main.go | sed 's/case //;s/"//g' | sort | tr '\n' ' '
echo
