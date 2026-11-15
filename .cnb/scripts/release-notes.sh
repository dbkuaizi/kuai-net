#!/usr/bin/env sh
set -eu
. ./.cnb/scripts/version.sh
mkdir -p release
{
  printf '# KuaiNet %s\n\n' "$VERSION"
  printf '单文件网络测速工具，支持浏览器与终端客户端。\n\n'
  printf '## 下载\n\n'
  printf '| 平台 | 文件 |\n|---|---|\n'
  printf '| Linux x64 | `kuai-net-linux-amd64.tar.gz` |\n'
  printf '| Linux ARM64 | `kuai-net-linux-arm64.tar.gz` |\n'
  printf '| Windows x64 | `kuai-net-windows-amd64.tar.gz` |\n'
  printf '| macOS Intel | `kuai-net-darwin-amd64.tar.gz` |\n'
  printf '| macOS Apple 芯片 | `kuai-net-darwin-arm64.tar.gz` |\n\n'
  printf '所有目标均禁用 CGO、无需额外资源文件；Linux 为静态链接，兼容 Alpine / Debian / Ubuntu。Linux 和 Windows 使用 UPX 压缩，macOS 为兼容性保留未压缩文件。\n\n'
  printf '## 使用\n\n```sh\nchmod +x kuai-net-linux-amd64\n'
  printf '# 服务端\n./kuai-net-linux-amd64 -addr :8080\n'
  printf '# 另一台机器上的 TUI 客户端，client / c 等效\n./kuai-net-linux-amd64 c\n```\n\n'
  printf '客户端先选语言，再通过鼠标或 Enter 展开配置字段，填写服务端地址、测试项、并发数、时长和单位。\n\n'
  printf '网页版底部链接区及终端界面标题后均显示同一构建版本号。\n\n'
  printf '测速时显示仪表盘与 ntcharts 实时曲线；历史表格支持查看详情，配置与最近 60 条记录保存在客户端本机。\n\n'
  printf '服务端启动后也可通过浏览器访问 `http://服务器IP:8080`。\n\n'
  printf 'Windows 在 PowerShell / Windows Terminal 中运行 `./kuai-net-windows-amd64.exe`，加 `c` 进入客户端。macOS 按芯片选择 darwin-amd64 / darwin-arm64，首次运行可能需在系统设置中允许。\n\n'
  printf '校验下载文件：`sha256sum -c SHA256SUMS.txt`。完整用法见 README。\n'
} > release/RELEASE_NOTES.md
