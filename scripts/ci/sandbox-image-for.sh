#!/usr/bin/env bash
# scripts/ci/sandbox-image-for.sh <image> [<ref>] — 打印与 <ref>（默认 HEAD）沙盒源码一致的
# 已构建镜像（<image>:<commit sha>）；找不到时退出码 1 并说明原因。
#
# build-sandbox 以推送头提交的完整 sha 给镜像打标签。<ref> 往回最后一次改动沙盒源码的
# 提交记为 X，则 X 及其后到 <ref> 的提交沙盒源码完全相同，它们中任何一个的镜像都与
# <ref> 对应。从 X 往后找：带镜像的通常就是 X 所在那次推送的头提交，一两次查询即命中。
# 发版据此晋升，而不是晋升当时恰好叫 :main 的镜像——后者在构建失败或尚未完成时会
# 悄悄指向旧版本。
set -euo pipefail
cd "$(dirname "$0")/../.."

image=${1:?用法: $0 <image> [<ref>]}
ref=${2:-HEAD}

inputs=()
while IFS= read -r p; do inputs+=("$p"); done < <(scripts/ci/sandbox-inputs.sh final)
last=$(git log -1 --format=%H "$ref" -- "${inputs[@]}")
if [ -z "$last" ]; then
  echo "在 $ref 的历史里找不到沙盒源码的提交（浅克隆？需要 fetch-depth: 0）" >&2
  exit 1
fi

# 从旧到新：X … <ref>（ancestry-path 只取 X 的后代，排除旁支）
candidates=("$last")
while IFS= read -r sha; do candidates+=("$sha"); done < <(git rev-list --reverse --ancestry-path "$last..$ref")
for sha in "${candidates[@]}"; do
  if docker buildx imagetools inspect "$image:$sha" >/dev/null 2>&1; then
    echo "$image:$sha"
    exit 0
  fi
done

echo "没有与 $ref 对应的沙盒镜像：最后一次沙盒源码改动是 ${last:0:8}，它及之后的 ${#candidates[@]} 个提交都没有镜像——检查 build-sandbox 是否失败" >&2
exit 1
