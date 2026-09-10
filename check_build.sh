#!/bin/bash
# 编译检查脚本 - 检查新架构代码是否能编译

set -e

echo "🔍 检查新架构代码编译..."

# 进入项目根目录
cd /Users/Xlbula/workspace/programs/go/liusha

echo ""
echo "1️⃣ 检查 Orchestrator..."
go build -o /dev/null ./internal/orchestrator/... 2>&1 | head -20 || true

echo ""
echo "2️⃣ 检查 Monitor..."
go build -o /dev/null ./internal/monitor/... 2>&1 | head -20 || true

echo ""
echo "3️⃣ 检查 Verifier..."
go build -o /dev/null ./internal/verifier/... 2>&1 | head -20 || true

echo ""
echo "4️⃣ 检查 Executor Pool..."
go build -o /dev/null ./internal/executor/pool.go 2>&1 | head -20 || true

echo ""
echo "5️⃣ 检查 WorldModel..."
go build -o /dev/null ./internal/worldmodel/... 2>&1 | head -20 || true

echo ""
echo "6️⃣ 检查 Planner..."
go build -o /dev/null ./internal/planner/... 2>&1 | head -20 || true

echo ""
echo "7️⃣ 检查 cmd/runner..."
go build -o /dev/null ./cmd/runner/... 2>&1 | head -20 || true

echo ""
echo "✅ 编译检查完成"
