#!/usr/bin/env bash
# build.sh —— sandbox-sdk-go v5（纯 v2）本地 / CI 通用构建脚本
#
# 产物（默认 ./dist/，资产名与滚动 Release "latest"、gosdk-update 消费方一致）：
#   sandbox-sdk-go-linux-arm64    # iSH / Apple Silicon / arm64 Linux
#   sandbox-sdk-go-linux-amd64    # x86_64 云沙箱 / GitHub Actions
#   SHA256SUMS                    # 两行 sha256（sha256sum 格式）
#
# 用法（脚本应放在工具源码目录，与 go.mod 同级）：
#   ./build.sh                        # 两个架构 + SHA256SUMS
#   VERSION=v5.0.1 ./build.sh         # 指定版本（注入 main.Version）
#   GOARCHS="arm64" ./build.sh        # 只构建指定架构（空格分隔，默认 "arm64 amd64"）
#   OUT_DIR=/tmp/out ./build.sh       # 指定产物目录（默认 ./dist）
#   SRC_DIR=/path/to/src ./build.sh   # 脚本不在源码目录时的显式指定（一般不需要）
#
# 版本注入：-ldflags "-X main.Version=<VERSION>"（main 包变量 Version，见 client.go）。
#   默认 v5.0.0；CI 经 VERSION 环境变量覆盖（见 .github/workflows/sandbox-sdk-go.yml）。
# 环境要求：Go 1.22+；CGO_ENABLED=0 纯静态交叉编译（iSH / Alpine 可直接运行）。
# 注意：iSH 本机没有 Go 工具链 —— 想验证构建请在 CubeSandbox 沙箱里执行，或交给 CI。
set -euo pipefail

SRC_DIR=${SRC_DIR:-$(cd "$(dirname "$0")" && pwd)}
cd "$SRC_DIR"

VERSION=${VERSION:-v5.0.0}
GOOS=${GOOS:-linux}
GOARCHS=${GOARCHS:-"arm64 amd64"}
OUT_DIR=${OUT_DIR:-dist}

if [ ! -f go.mod ] || ! ls ./*.go >/dev/null 2>&1; then
  echo "!! 在 $SRC_DIR 未找到 Go 源码（需要 go.mod 与 *.go）；build.sh 应放在工具源码目录" >&2
  exit 1
fi
command -v go >/dev/null 2>&1 || {
  echo "!! 未找到 go 命令（需要 Go 1.22+）；请在 CubeSandbox 沙箱或 CI 里执行本脚本" >&2
  exit 1
}

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

echo "[sandbox-sdk-go v5] 版本=$VERSION 目标=$GOOS/{${GOARCHS// /,}} 输出=$OUT_DIR/"
for arch in $GOARCHS; do
  out="$OUT_DIR/sandbox-sdk-go-$GOOS-$arch"
  echo "[build] $GOOS/$arch -> $out"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$out" .
done

( cd "$OUT_DIR" && sha256sum sandbox-sdk-go-* > SHA256SUMS )
echo "[sums] $(wc -l < "$OUT_DIR/SHA256SUMS") 行校验和 -> $OUT_DIR/SHA256SUMS"

# 冒烟自检：只跑「本机架构 == 目标架构」的那份（交叉编译的另一份本机无法执行）
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
for arch in $GOARCHS; do
  if [ "$GOOS" = "$host_os" ] && [ "$arch" = "$host_arch" ]; then
    echo "[smoke] $OUT_DIR/sandbox-sdk-go-$GOOS-$arch version"
    "$OUT_DIR/sandbox-sdk-go-$GOOS-$arch" version | head -1 || true
  fi
done

echo "[done] 产物："
ls -l "$OUT_DIR"
