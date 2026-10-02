#!/usr/bin/env bash
# cubesandbox/build.sh —— 构建 cubesandbox-sdk-go（封装官方 TencentCloud/CubeSandbox Go SDK）
#
# 用法：
#   ./build.sh                     # 默认 linux/arm64
#   GOARCH=amd64 ./build.sh        # 交叉编译 amd64
#   OUT=/tmp/x ./build.sh          # 指定输出名
#
# 触发 CI 单工具构建（推送后可只构建这一支）：
#   GitHub → Actions → build-tools → Run workflow → tool = cubesandbox
set -euo pipefail
cd "$(dirname "$0")"

GOOS=${GOOS:-linux}
GOARCH=${GOARCH:-$(go env GOARCH)}
OUT=${OUT:-cubesandbox-sdk-go-${GOOS}-${GOARCH}}

echo "[cubesandbox] go mod tidy"
go mod tidy

echo "[cubesandbox] build $GOOS/$GOARCH -> $OUT"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  go build -trimpath -ldflags="-s -w" -o "$OUT" .

ls -l "$OUT"
"$OUT" version 2>/dev/null | head -1 || true
