#!/usr/bin/env bash
# 构建官方优选 APK
#
# 用法：
#   ./build-apk.sh            # debug 包
#   ./build-apk.sh release    # release 包（需要 android/store.properties）
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
. "$SCRIPT_DIR/env.sh"

BUILD_TYPE="${1:-debug}"
case "$BUILD_TYPE" in
  debug|release) ;;
  *) echo "[build] 未知构建类型：$BUILD_TYPE（可选 debug / release）"; exit 1 ;;
esac

GOMOBILE="$GOPATH/bin/gomobile"

command -v go >/dev/null 2>&1 || { echo "[build] go not found in PATH"; exit 1; }
command -v gradle >/dev/null 2>&1 || { echo "[build] gradle not found in PATH"; exit 1; }

# gomobile 不一定装过，缺了就补
if [ ! -x "$GOMOBILE" ]; then
  echo "[build] 安装 gomobile..."
  go install golang.org/x/mobile/cmd/gomobile@latest
  go install golang.org/x/mobile/cmd/gobind@latest
  "$GOMOBILE" init
fi

# 出包前先跑测试，避免把明显坏掉的核心层打进 APK
echo "[build] 运行 Go 测试..."
cd "$SCRIPT_DIR"
gofmt -l . | tee /tmp/gf-fmt.txt
if [ -s /tmp/gf-fmt.txt ]; then
  echo "[build] 上述文件未格式化，先跑 gofmt -w ."
  exit 1
fi
go vet ./...
go test ./...

printf "sdk.dir=%s\n" "$ANDROID_HOME" > "$SCRIPT_DIR/android/local.properties"

echo "[build] 生成 AAR..."
"$GOMOBILE" bind \
  -target=android \
  -androidapi 24 \
  -javapkg com.cf.ip \
  -trimpath \
  -ldflags="-s -w" \
  -o "$SCRIPT_DIR/android/app/libs/cfip.aar" \
  "$SCRIPT_DIR"

if [ "$BUILD_TYPE" = "release" ] && [ ! -f "$SCRIPT_DIR/android/store.properties" ]; then
  echo "[build] 缺少 android/store.properties，无法签名 release 包"
  echo "[build] 格式见 README.md"
  exit 1
fi

echo "[build] 编译 APK（$BUILD_TYPE）..."
cd "$SCRIPT_DIR/android"
if [ "$BUILD_TYPE" = "release" ]; then
  gradle clean assembleRelease --no-daemon
  OUT="app/build/outputs/apk/release"
else
  gradle clean assembleDebug --no-daemon
  OUT="app/build/outputs/apk/debug"
fi

echo "[build] 完成，产物："
ls -lh "$OUT"/*.apk 2>/dev/null || true
