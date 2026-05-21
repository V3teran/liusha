#!/usr/bin/env bash
# scripts/dev/down.sh — 关基础设施（postgres + redis + proxify）
# 不删 volume，下次起仍保留数据；要彻底重置加 -v

set -euo pipefail
cd "$(dirname "$0")/../.."

KEEP_DATA=true
if [ "${1:-}" = "-v" ] || [ "${1:-}" = "--reset" ]; then
  KEEP_DATA=false
fi

echo "===== 关基础设施 ====="
if [ "$KEEP_DATA" = "true" ]; then
  docker compose -f deployments/docker-compose.yml --profile proxy --profile e2e --profile agent down 2>&1 | tail -10
  echo ""
  echo "✓ 容器关闭，数据保留（pg/redis volume）"
  echo "  下次起：./scripts/dev/up.sh"
  echo "  彻底重置（删 volume）：./scripts/dev/down.sh -v"
else
  docker compose -f deployments/docker-compose.yml --profile proxy --profile e2e --profile agent down -v 2>&1 | tail -10
  echo ""
  echo "✓ 容器关闭，数据已清（下次起需重新 migrate）"
fi
