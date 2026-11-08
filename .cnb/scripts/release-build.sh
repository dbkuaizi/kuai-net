#!/usr/bin/env sh
# 交叉编译三个平台的五个目标并打包。运行于 golang:1.26-alpine 镜像。
set -eu

APP=kuai-net
mkdir -p release

# 先确认工具链版本满足 go.mod 要求，避免出现含糊的 "go.mod requires go >= x.y" 失败
REQ_GO="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"
CUR_GO="$(go env GOVERSION | sed 's/^go//')"
echo "--- go.mod 要求 Go ${REQ_GO}，当前工具链 ${CUR_GO}"
LOWEST="$(printf '%s\n%s\n' "$REQ_GO" "$CUR_GO" | sort -V | head -n 1)"
if [ "$LOWEST" != "$REQ_GO" ]; then
  echo "当前 Go ${CUR_GO} 低于 go.mod 要求的 ${REQ_GO}，请更换构建镜像（如 golang:${REQ_GO}-alpine）" >&2
  exit 1
fi

# alpine 基础镜像默认没有 upx，按需安装（失败不中断，只是不压缩）
if command -v apk >/dev/null 2>&1 && ! command -v upx >/dev/null 2>&1; then
  apk add --no-cache upx >/dev/null 2>&1 || true
fi

LDFLAGS="-s -w -buildid="

build() {
  os="$1"
  arch="$2"
  out="$3"
  echo "--- building ${os}/${arch} -> ${out}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -buildvcs=false -ldflags="$LDFLAGS" -o "release/${out}" .
}

# ---------- 编译 ----------
build linux   amd64 "${APP}-linux-amd64"
build linux   arm64 "${APP}-linux-arm64"
build windows amd64 "${APP}-windows-amd64.exe"
build darwin  amd64 "${APP}-darwin-amd64"
build darwin  arm64 "${APP}-darwin-arm64"

# ---------- 压缩（有 upx 就压，没有就跳过） ----------
if command -v upx >/dev/null 2>&1; then
  echo "--- upx -9"
  for f in "release/${APP}-linux-amd64" \
           "release/${APP}-linux-arm64" \
           "release/${APP}-windows-amd64.exe" \
           "release/${APP}-darwin-amd64" \
           "release/${APP}-darwin-arm64"; do
    # Mach-O 对 upx 支持不稳定，跳过 darwin；windows/linux 压缩效果好
    case "$f" in
      *darwin*) echo "  skip upx: $(basename "$f")" ;;
      *) upx -9 --no-progress "$f" || echo "  upx 失败，保留原文件: $f" ;;
    esac
  done
else
  echo "--- upx 不可用，跳过压缩"
fi

# ---------- 打包 ----------
# 统一用 tar.gz：Windows 10+ 资源管理器原生支持，也保留 Linux/macOS 的可执行权限
pack() {
  bin="$1"
  name="$(basename "$bin")"
  base="${name%.exe}"
  echo "--- ${base}.tar.gz"
  tar -czf "release/${base}.tar.gz" -C release "${name}"
  rm -f "release/${name}"
}

pack "release/${APP}-linux-amd64"
pack "release/${APP}-linux-arm64"
pack "release/${APP}-windows-amd64.exe"
pack "release/${APP}-darwin-amd64"
pack "release/${APP}-darwin-arm64"

# ---------- 校验和 ----------
echo "--- checksums"
( cd release && sha256sum ./*.tar.gz > SHA256SUMS.txt && cat SHA256SUMS.txt )

# ---------- 汇总 ----------
echo "--- artifacts"
ls -lh release/
