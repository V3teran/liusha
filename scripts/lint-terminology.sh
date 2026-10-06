#!/usr/bin/env bash
# lint-terminology.sh：术语契约门禁。
#
# 用法：
#   scripts/lint-terminology.sh            # 检查模式：发现残留退出 1（CI / make lint 用）
#   scripts/lint-terminology.sh count      # 报数模式：仅输出各术语残留计数（循环自检用）
#
# 规则与豁免清单即下方 FORBIDDEN 列表（含语义备忘）。
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# 扫描范围：代码 + 文档 + 配置；排除历史迁移（不可编辑）、契约保留区、第三方与本脚本自身。
SCAN_ARGS=(
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.md'
  --include='*.yaml' --include='*.yml' --include='*.json'
  internal/ cmd/ web/src db/ agents/ skills/ config/ deployments/ docs/ Makefile
)

# 排除路径（契约保留区 / 工具自身 / 第三方产物）
EXCLUDES=(
  ':!db/migrations/**'      # 历史迁移不可编辑
  ':!deployments/**'        # 容器镜像契约（LIUSHA_AGENT_ID 等）
  ':!scripts/lint-terminology.sh'
  ':!web/dist/**' ':!**/node_modules/**'
)

# 禁用术语（旧架构残留，全称匹配大小写不敏感）。
# 注意：lead（情报线索）/ lesson（历史沿革注释）/ engagement（英文领域用语）为允许术语，不进禁用表。
FORBIDDEN=(
  '知识图谱'
  '认知图'
  '世界模型'
  'knowledge[-_ ]\?graph'
  'KnowledgeGraph'
  'knowledgegraph'
  'KnowledgeGraphPage'
  'worldmodel'
  'WorldModel'
)

MODE="${1:-check}"

total=0
for term in "${FORBIDDEN[@]}"; do
  hits=$(git grep -i -E "$term" -- "${SCAN_ARGS[@]}" "${EXCLUDES[@]}" 2>/dev/null | wc -l | tr -d ' ')
  if [ "$hits" != "0" ]; then
    echo "残留 [$term]: $hits"
    if [ "$MODE" != "count" ]; then
      git grep -i -n -E "$term" -- "${SCAN_ARGS[@]}" "${EXCLUDES[@]}" 2>/dev/null | head -10
    fi
    total=$((total + hits))
  fi
done

echo "TOTAL_RESIDUAL=$total"
if [ "$MODE" != "count" ] && [ "$total" != "0" ]; then
  echo "术语门禁未通过：存在废弃术语残留，见上方清单。"
  exit 1
fi
exit 0
