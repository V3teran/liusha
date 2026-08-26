#!/bin/bash
# P2 运行时验证脚本

set -e

echo "=========================================="
echo "P2: 运行时验证"
echo "=========================================="
echo ""

# 检查 PostgreSQL 连接
echo "1. 检查 PostgreSQL 连接..."
if ! psql -U postgres -h localhost -c "SELECT 1" > /dev/null 2>&1; then
    echo "❌ PostgreSQL 未运行或连接失败"
    echo "   请确保 PostgreSQL 运行在 localhost:5432"
    exit 1
fi
echo "✅ PostgreSQL 连接正常"
echo ""

# 创建测试数据库
echo "2. 创建测试数据库..."
psql -U postgres -h localhost -c "DROP DATABASE IF EXISTS liusha_test" > /dev/null 2>&1 || true
psql -U postgres -h localhost -c "CREATE DATABASE liusha_test" > /dev/null 2>&1
echo "✅ 测试数据库创建完成"
echo ""

# 执行 migrations
echo "3. 执行 migrations..."
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/liusha_test?sslmode=disable"

# 检查是否有 migrate 工具
if ! command -v migrate &> /dev/null; then
    echo "❌ migrate 工具未安装"
    echo "   安装方法: brew install golang-migrate"
    exit 1
fi

cd "$(dirname "$0")/../.."
migrate -path db/migrations -database "$DATABASE_URL" up
echo "✅ Migrations 执行完成"
echo ""

# 验证 lead 表
echo "4. 验证 lead 表结构..."
psql "$DATABASE_URL" -c "\d lead" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✅ lead 表创建成功"
    psql "$DATABASE_URL" -c "\d lead" | grep -E "assignment_id|kind|detail"
else
    echo "❌ lead 表不存在"
    exit 1
fi
echo ""

# 验证 wm_node 表
echo "5. 验证 wm_node 表结构..."
psql "$DATABASE_URL" -c "\d wm_node" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✅ wm_node 表存在"
    psql "$DATABASE_URL" -c "\d wm_node" | grep -E "task_id|kind|state|complexity"
else
    echo "❌ wm_node 表不存在"
    exit 1
fi
echo ""

# 运行集成测试
echo "6. 运行集成测试..."
echo ""

echo "6.1 测试 Lead 黑板隔离..."
go test -v ./internal/lead -run TestAssignmentIsolation
go test -v ./internal/lead -run TestCrossTaskSharing
go test -v ./internal/lead -run TestReadRecentGrouping
go test -v ./internal/lead -run TestPersistence
echo "✅ Lead 黑板测试通过"
echo ""

echo "6.2 测试世界模型隔离..."
go test -v ./internal/worldmodel -run TestTaskIsolation
go test -v ./internal/worldmodel -run TestCompleteDataFlow
go test -v ./internal/worldmodel -run TestMoveDependency
echo "✅ 世界模型测试通过"
echo ""

# 清理测试数据库
echo "7. 清理测试数据库..."
psql -U postgres -h localhost -c "DROP DATABASE IF EXISTS liusha_test" > /dev/null 2>&1
echo "✅ 清理完成"
echo ""

echo "=========================================="
echo "✅ P2 运行时验证全部通过！"
echo "=========================================="
