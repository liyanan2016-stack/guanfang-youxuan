#!/usr/bin/env bash
# 编译环境配置 - 官方优选（guanfang-youxuan）
#
# 原版硬编码 ROOT_DIR=/root/demo，换机器就跑不起来。
# 改成按优先级自动探测，也可用环境变量覆盖：
#   GF_SDK_ROOT=/path/to/sdk-parent ./build-apk.sh
#
# 期望的目录结构（任选其一存在即可）：
#   $GF_SDK_ROOT/sdk/{jdk,android-sdk,go 或 go-root,gradle 或 gradle-8.9}
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# 探测顺序：显式指定 > 沙盒常用位置 > 原版默认
for candidate in "${GF_SDK_ROOT:-}" /tmp/cfip /root/demo; do
  [ -n "$candidate" ] && [ -d "$candidate/sdk" ] && ROOT_DIR="$candidate" && break
done

if [ -z "${ROOT_DIR:-}" ]; then
  echo "[env] 找不到工具链目录（需要 <root>/sdk/）"
  echo "[env] 用 GF_SDK_ROOT 指定，例如：GF_SDK_ROOT=/opt/toolchain ./build-apk.sh"
  return 1 2>/dev/null || exit 1
fi
echo "[env] ROOT_DIR: $ROOT_DIR"

SDK="$ROOT_DIR/sdk"

export JAVA_HOME="$SDK/jdk"
export ANDROID_HOME="$SDK/android-sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"

# NDK 版本目录名会变，取实际存在的第一个而不是写死版本号
if [ -d "$ANDROID_HOME/ndk" ]; then
  _ndk=$(find "$ANDROID_HOME/ndk" -maxdepth 1 -mindepth 1 -type d | sort -V | tail -1)
  [ -n "$_ndk" ] && export ANDROID_NDK_HOME="$_ndk"
fi

# go 可能装在 sdk/go 或 sdk/go-root
for d in "$SDK/go-root" "$SDK/go"; do
  [ -x "$d/bin/go" ] && _gobin="$d/bin" && break
done

# gradle 同理
for d in "$SDK/gradle" "$SDK/gradle-8.9"; do
  [ -x "$d/bin/gradle" ] && _gradlebin="$d/bin" && break
done

export GOPATH="${GOPATH:-$SDK/gopath}"
[ -d "$SDK/gomod" ] && export GOMODCACHE="$SDK/gomod"
export GOCACHE="${GOCACHE:-$SCRIPT_DIR/.cache/go-build}"
export GOMOBILECACHE="$SCRIPT_DIR/.cache/gomobile"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$SCRIPT_DIR/.gradle}"
export TMPDIR="${TMPDIR:-$SCRIPT_DIR/.tmp}"

export PATH="$JAVA_HOME/bin:${_gobin:-}:${_gradlebin:-}:$GOPATH/bin:$ANDROID_HOME/platform-tools:$PATH"

mkdir -p "$GRADLE_USER_HOME" "$GOCACHE" "$GOMOBILECACHE" "$TMPDIR" \
         "$SCRIPT_DIR/android/app/libs" "$GOPATH"

echo "[env] java:    $(java -version 2>&1 | head -1)"
echo "[env] go:      $(go version 2>&1)"
echo "[env] gradle:  $(gradle --version 2>&1 | grep 'Gradle ' | head -1)"
echo "[env] ANDROID_HOME: $ANDROID_HOME"
echo "[env] ANDROID_NDK_HOME: ${ANDROID_NDK_HOME:-未找到}"
