#!/usr/bin/env sh
# 构建 Linux / Windows / macOS 单文件；Linux、Windows 必须通过 UPX 压缩与校验。
set -eu

APP=kuai-net
. ./.cnb/scripts/version.sh
printf '构建 KuaiNet %s (Linux / Windows / macOS)\n' "$VERSION"
mkdir -p release

REQ_GO="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"
CUR_GO="$(go env GOVERSION | sed 's/^go//')"
LOWEST="$(printf '%s\n%s\n' "$REQ_GO" "$CUR_GO" | sort -V | head -n 1)"
if [ "$LOWEST" != "$REQ_GO" ]; then
  echo "需要 Go ${REQ_GO}，当前为 ${CUR_GO}" >&2
  exit 1
fi

if ! command -v upx >/dev/null 2>&1; then
  if command -v apk >/dev/null 2>&1; then
    apk add --no-cache upx
  else
    echo "构建需要 UPX，请先安装 upx" >&2
    exit 1
  fi
fi

for target in linux-amd64 linux-arm64 windows-amd64 darwin-amd64 darwin-arm64; do
  os="${target%-*}"
  arch="${target#*-}"
  name="${APP}-${target}"
  if [ "$os" = windows ]; then
    name="${name}.exe"
  fi
  binary="release/${name}"
  printf '\n--- %s/%s -> %s\n' "$os" "$arch" "$binary"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
    -o "$binary" .
  if [ "$os" = darwin ]; then
    echo 'macOS: 保留未压缩的 Mach-O 文件，避免 UPX 兼容性问题'
  else
    upx -9 --no-progress "$binary"
    upx -t "$binary"
  fi
  chmod +x "$binary"
  tar -czf "release/${APP}-${target}.tar.gz" -C release "$name"
done

# 明确列出本次发布的五个目标，避免旧的归档混入清单。
(
  cd release
  sha256sum \
    "${APP}-linux-amd64.tar.gz" \
    "${APP}-linux-arm64.tar.gz" \
    "${APP}-windows-amd64.tar.gz" \
    "${APP}-darwin-amd64.tar.gz" \
    "${APP}-darwin-arm64.tar.gz" > SHA256SUMS.txt
)
ls -lh release/
