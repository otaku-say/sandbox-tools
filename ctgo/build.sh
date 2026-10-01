#!/bin/sh
# 双架构交叉编译 ctgo。
#
# 在任意装有 Go 1.22+ 的 Linux 上执行即可（iSH 本身没有 Go 工具链，
# 可交由云沙箱代编，产物静态链接、无 libc 依赖，可直接拷回 iSH 运行）。
set -e
cd "$(dirname "$0")"

mkdir -p dist
export CGO_ENABLED=0

for arch in amd64 arm64; do
    echo "building linux/$arch ..."
    GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" \
        -o "dist/ctgo-linux-$arch" .
done

( cd dist && sha256sum ctgo-linux-amd64 ctgo-linux-arm64 > SHA256SUMS )
echo "--- dist/SHA256SUMS ---"
cat dist/SHA256SUMS
