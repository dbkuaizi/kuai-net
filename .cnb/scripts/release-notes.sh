#!/usr/bin/env sh
# 生成 Release 说明。运行于 alpine 镜像，只用 POSIX sh。
set -eu

VERSION="${CNB_BRANCH:-v0.0.0}"
mkdir -p release

{
  printf '# KuaiNet %s\n\n' "$VERSION"
  printf '基于 Go 语言的极简内网服务器测速工具。启动后在浏览器打开页面，即可测量服务器与浏览器之间的下载、上传速度和延迟。\n\n'

  printf '## 特点\n\n'
  printf '%s\n' '- 单个可执行文件，前端资源全部内嵌，无需额外安装'
  printf '%s\n' '- 剔除 TCP 慢启动与收尾抖动，只取稳定区间算速度'
  printf '%s\n' '- 曲线经滑动平均 + 指数平滑处理，无锯齿'
  printf '%s\n\n' '- 支持简体中文在内的 10 种语言，深浅色主题'

  printf '## 下载\n\n'
  printf '| 平台 | 文件 |\n'
  printf '|---|---|\n'
  printf '%s\n' '| Linux x64 | `kuai-net-linux-amd64.tar.gz` |'
  printf '%s\n' '| Linux ARM64 | `kuai-net-linux-arm64.tar.gz` |'
  printf '%s\n' '| Windows x64 | `kuai-net-windows-amd64.tar.gz` |'
  printf '%s\n' '| macOS Intel | `kuai-net-darwin-amd64.tar.gz` |'
  printf '%s\n\n' '| macOS Apple 芯片 | `kuai-net-darwin-arm64.tar.gz` |'

  printf '以上均为终端程序，解压后直接运行：\n\n'
  printf '```bash\n'
  printf './kuai-net -addr :8080\n'
  printf '```\n\n'
  printf '然后在浏览器打开 `http://服务器IP:8080`。\n\n'

  printf '## 说明\n\n'
  printf '%s\n' '- macOS / Linux 为静态编译，解压后如无执行权限，先 `chmod +x`'
  printf '%s\n' '- macOS 首次运行若提示安全限制，可在「系统设置 → 隐私与安全性」中允许'
  printf '%s\n' '- 同一批产物会同步上传到 GitHub Release，GitHub 不单独构建'
  printf '%s\n\n' '- 校验文件完整性：`sha256sum -c SHA256SUMS.txt`'

  printf '完整参数与部署说明见仓库 README。\n'
} > release/RELEASE_NOTES.md

echo "--- release notes"
cat release/RELEASE_NOTES.md
