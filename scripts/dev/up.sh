#!/usr/bin/env bash
# scripts/dev/up.sh — 起基础设施（postgres + redis + proxify），然后 migrate
# host 侧 vulnapp/api/scanner 不在本脚本启动（用 run-svc.sh）

set -euo pipefail
cd "$(dirname "$0")/../.."

echo "===== 1. 起 docker 基础设施（postgres + redis + proxify） ====="
docker compose -f deployments/docker-compose.yml up -d postgres redis
docker compose -f deployments/docker-compose.yml --profile proxy up -d proxify

echo ""
echo "===== 2. 等 healthcheck（最多 30s） ====="
for i in {1..15}; do
  s=$(docker compose -f deployments/docker-compose.yml ps --format "{{.Status}}" 2>/dev/null | grep -c "healthy" || true)
  if [ "$s" -ge 2 ]; then
    echo "  ✓ pg + redis healthy"
    break
  fi
  sleep 2
done

echo ""
echo "===== 3. 跑 migrate ====="
make migrate

echo ""
echo "===== 4. 当前状态 ====="
docker compose -f deployments/docker-compose.yml ps --format "table {{.Name}}\t{{.Status}}"

echo ""
echo "✓ 基础设施就绪。下一步："
echo "    ./scripts/dev/run-svc.sh        # 起 vulnapp + api + scanner（host 侧）"
echo "    ./scripts/dev/e2e.sh [bac|sqli] # 跑 e2e 触发器（profile 默认 bac，另一个终端）"
echo "    ./scripts/dev/down.sh           # 关基础设施"
