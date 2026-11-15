#!/usr/bin/env sh
# 构建后校验产物齐全，缺任何一个就立刻失败并打印已有内容，便于排查。
set -eu

echo "=== release/ 目录内容 ==="
ls -lh release/ || { echo "release/ 目录不存在" >&2; exit 1; }

missing=0
for f in \
  release/kuai-net-linux-amd64.tar.gz \
  release/kuai-net-linux-arm64.tar.gz \
  release/kuai-net-windows-amd64.tar.gz \
  release/kuai-net-darwin-amd64.tar.gz \
  release/kuai-net-darwin-arm64.tar.gz \
  release/SHA256SUMS.txt
do
  if [ -f "$f" ]; then
    echo "OK   $f"
  else
    echo "MISS $f" >&2
    missing=$((missing + 1))
  fi
done

if [ "$missing" -gt 0 ]; then
  echo "缺少 ${missing} 个产物，构建中断" >&2
  exit 1
fi

echo "=== 校验和 ==="
(cd release && sha256sum -c SHA256SUMS.txt)

echo "=== 校验归档内容 ==="
for target in linux-amd64 linux-arm64 windows-amd64 darwin-amd64 darwin-arm64; do
  name="kuai-net-${target}"
  if [ "$target" = windows-amd64 ]; then
    name="${name}.exe"
  fi
  contents="$(tar -tzf "release/kuai-net-${target}.tar.gz")"
  if [ "$contents" != "$name" ]; then
    echo "归档内容不正确：${target}: ${contents}" >&2
    exit 1
  fi
done
echo "=== 全部产物就位 ==="
