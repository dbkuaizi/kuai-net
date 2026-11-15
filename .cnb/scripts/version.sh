#!/usr/bin/env sh
# Shared build/release version. CI tags and explicit VERSION override Git.
VERSION="${VERSION:-${CNB_BRANCH:-}}"
if [ -z "$VERSION" ]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || printf 'dev')"
fi
# Keep the value safe for Go linker flags and release headings.
case "$VERSION" in
  ''|*[!a-zA-Z0-9._+-]*)
    echo "无效的版本号：${VERSION}" >&2
    exit 1
    ;;
esac
