#!/usr/bin/env bash
# scripts/ci/sandbox-inputs.sh base|final — 打印沙盒镜像的源码路径（git pathspec，每行一个）。
#
# 唯一事实源是 Dockerfile 本身：Dockerfile + 它 COPY/ADD 进镜像的仓库路径（--from 多阶段
# 拷贝不算仓库输入）。final 基于 base 构建，其输入包含 base 的输入。
# build-sandbox 据此判定是否重建，release-images 据此找出与发版代码对应的沙盒镜像——
# 两边同源，改 Dockerfile 不用再同步任何路径清单。
set -euo pipefail
cd "$(dirname "$0")/../.."

DIR=deployments/tool-images/sandbox

copy_sources() {
  local dockerfile=$1
  echo "$dockerfile"
  awk '
    toupper($1) == "COPY" || toupper($1) == "ADD" {
      n = 0
      for (i = 2; i <= NF; i++) {
        if ($i ~ /^--from=/) next      # 多阶段拷贝：来源不是仓库
        if ($i ~ /^--/) continue       # 其余标志（--chown 等）
        args[++n] = $i
      }
      for (i = 1; i < n; i++) print args[i]   # 最后一个是目标路径
    }
  ' "$dockerfile"
}

case "${1:-}" in
  base) copy_sources "$DIR/Dockerfile.base" ;;
  final) { copy_sources "$DIR/Dockerfile.base"; copy_sources "$DIR/Dockerfile.final"; } | sort -u ;;
  *) echo "用法: $0 base|final" >&2; exit 2 ;;
esac
