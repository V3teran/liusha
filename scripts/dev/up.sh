#!/usr/bin/env bash
# scripts/dev/up.sh — 起本地开发依赖（postgres + redis + 沙盒容器）并迁移数据库。
# 组件（proxy / api / runner）在宿主上直接运行，见 run-svc.sh。
# 首次起沙盒会本地构建沙盒镜像（Kali + 工具，较久），之后命中缓存。

set -euo pipefail
cd "$(dirname "$0")/../.."

echo "===== 1. 起依赖容器（postgres + redis + sandbox）====="
# 沙盒的浏览器流量回灌到宿主上运行的 runner（compose 缺省指向容器内的 runner 服务）。
export LIUSHA_SANDBOX_INGEST_URL="http://host.docker.internal:8590/internal/v1/traffic/ingest"
docker compose up -d --build postgres redis sandbox

echo ""
echo "===== 2. 等健康检查 ====="
for svc in liusha-postgres liusha-redis liusha-sandbox; do
  for _ in $(seq 1 60); do
    [ "$(docker inspect -f '{{.State.Health.Status}}' "$svc" 2>/dev/null)" = "healthy" ] && break
    sleep 2
  done
  echo "  $svc: $(docker inspect -f '{{.State.Health.Status}}' "$svc" 2>/dev/null || echo 未运行)"
done

echo ""
echo "===== 3. 迁移数据库 ====="
make migrate

echo ""
echo "✓ 依赖就绪。下一步："
echo "    ./scripts/dev/run-svc.sh        # 宿主上起 proxy + api + runner"
echo "    ./scripts/dev/e2e.sh [xss|full]  # 跑端到端剧本（另一个终端）"
echo "    ./scripts/dev/down.sh           # 关依赖容器"
