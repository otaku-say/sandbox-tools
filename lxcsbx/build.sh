#!/bin/bash
# 交叉编译 lxcsbx（静态，两架构）
set -e
export CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod
for arch in amd64 arm64; do
  GOARCH=$arch go build -trimpath -ldflags="-s -w" -o lxcsbx-linux-$arch .
  echo "built lxcsbx-linux-$arch"
done
sha256sum lxcsbx-linux-amd64 lxcsbx-linux-arm64 > SHA256SUMS
