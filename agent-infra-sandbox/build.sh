#!/usr/bin/env bash
# agent-infra-sandbox/build.sh —— 构建 sandbox-sdk-go（封装 agent-infra/sandbox 的 Go SDK）
#
# 用法：
#   ./build.sh                     # 默认 linux/arm64
#   GOARCH=amd64 ./build.sh
#   OUT=/tmp/x ./build.sh
#
# 触发 CI 单工具构建：
#   GitHub → Actions → build-tools → Run workflow → tool = agent-infra-sandbox
set -euo pipefail
cd "$(dirname "$0")"

GOOS=${GOOS:-linux}
GOARCH=${GOARCH:-$(go env GOARCH)}
OUT=${OUT:-sandbox-sdk-go-${GOOS}-${GOARCH}}

echo "[agent-infra-sandbox] go mod tidy"
go mod tidy

echo "[agent-infra-sandbox] build $GOOS/$GOARCH -> $OUT"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  go build -trimpath -ldflags="-s -w" -o "$OUT" .

ls -l "$OUT"
"$OUT" version 2>/dev/null | head -1 || true
