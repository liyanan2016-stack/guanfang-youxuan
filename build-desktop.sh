#!/usr/bin/env bash
# 构建官方优选桌面版（Windows / Linux）
#
# 用法：
#   ./build-desktop.sh              # 全部目标
#   ./build-desktop.sh windows-amd64
#
# 产物在 dist/ 下。桌面版是单文件可执行程序，
# 界面用内嵌的本地网页，只监听 127.0.0.1，不对局域网开放。
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
DIST="$SCRIPT_DIR/dist"
PKG="./cmd/guanfang"

# 复用 env.sh 里的 Go 工具链探测；桌面版不需要 Android SDK，
# 所以 env.sh 找不到 SDK 也不该卡住构建
if [ -f "$SCRIPT_DIR/env.sh" ]; then
  . "$SCRIPT_DIR/env.sh" || true
fi

command -v go >/dev/null 2>&1 || { echo "[build] go not found in PATH"; exit 1; }

VERSION=$(sed -n 's/^const libVersion = "\(.*\)"$/\1/p' "$SCRIPT_DIR/better.go")
[ -n "$VERSION" ] || { echo "[build] 无法从 better.go 读取版本号"; exit 1; }
echo "[build] 版本: $VERSION"

cd "$SCRIPT_DIR"
echo "[build] 检查代码..."
FMT=$(gofmt -l .)
if [ -n "$FMT" ]; then
  echo "[build] 以下文件未格式化，先跑 gofmt -w ."
  echo "$FMT"
  exit 1
fi
go vet ./...
go test ./...

mkdir -p "$DIST"

build_one() {
  local goos=$1 goarch=$2 out=$3
  echo "[build] $goos/$goarch -> $out"
  # -s -w 去掉符号表和调试信息，体积小一半
  # windowsgui 隐藏控制台窗口？—— 不加：桌面版要靠控制台显示地址和 Ctrl+C 退出
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$DIST/$out" "$PKG"
}

TARGET="${1:-all}"
case "$TARGET" in
  all)
    build_one windows amd64 "guanfang-youxuan-v${VERSION}-windows-amd64.exe"
    build_one windows arm64 "guanfang-youxuan-v${VERSION}-windows-arm64.exe"
    build_one linux   amd64 "guanfang-youxuan-v${VERSION}-linux-amd64"
    ;;
  windows-amd64) build_one windows amd64 "guanfang-youxuan-v${VERSION}-windows-amd64.exe" ;;
  windows-arm64) build_one windows arm64 "guanfang-youxuan-v${VERSION}-windows-arm64.exe" ;;
  linux-amd64)   build_one linux   amd64 "guanfang-youxuan-v${VERSION}-linux-amd64" ;;
  *)
    echo "[build] 未知目标：$TARGET"
    echo "[build] 可选：all / windows-amd64 / windows-arm64 / linux-amd64"
    exit 1
    ;;
esac

echo "[build] 完成，产物："
ls -lh "$DIST"
( cd "$DIST" && sha256sum ./* )
