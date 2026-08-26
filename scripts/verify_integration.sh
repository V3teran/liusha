#!/bin/bash
# 验证 PlannerAgent + ExecutionLoop 集成是否正确

set -e

echo "=== 验证集成组件 ==="

# 1. 检查编译
echo "1. 检查编译..."
cd "$(dirname "$0")/.."
go build ./cmd/runner/... || { echo "❌ runner 编译失败"; exit 1; }
go build ./cmd/api/... || { echo "❌ api 编译失败"; exit 1; }
echo "✅ 编译通过"

# 2. 检查关键接口实现
echo ""
echo "2. 检查接口实现..."

# 验证 worldmodel.Store 实现 WorldView 接口
grep -q "func.*ListNodes" internal/worldmodel/*.go || {
    echo "❌ worldmodel.Store 缺少 ListNodes 方法"
    exit 1
}
grep -q "func.*ListEdges" internal/worldmodel/*.go || {
    echo "❌ worldmodel.Store 缺少 ListEdges 方法"
    exit 1
}
grep -q "func.*ListMoves" internal/worldmodel/*.go || {
    echo "❌ worldmodel.Store 缺少 ListMoves 方法"
    exit 1
}
echo "✅ worldmodel.Store 实现 WorldView 接口"

# 3. 检查 EventBus 事件发布
echo ""
echo "3. 检查 EventBus 事件..."
grep -q "PublishMoveCompleted" internal/cognition/event.go || {
    echo "❌ EventBus 缺少 PublishMoveCompleted"
    exit 1
}
grep -q "PublishTaskStarted" internal/cognition/event.go || {
    echo "❌ EventBus 缺少 PublishTaskStarted"
    exit 1
}
echo "✅ EventBus 事件完整"

# 4. 检查 ExecutionLoop 事件发布
echo ""
echo "4. 检查 ExecutionLoop 事件发布..."
grep -q "eventBus.PublishMoveCompleted" internal/cognition/execution_loop.go || {
    echo "❌ ExecutionLoop 未发布 MoveCompleted 事件"
    exit 1
}
echo "✅ ExecutionLoop 正确发布事件"

# 5. 检查 PlannerAgent 启动
echo ""
echo "5. 检查 PlannerAgent 集成..."
grep -q "cognition.NewPlannerAgent" cmd/runner/cognition.go || {
    echo "❌ runner 未启动 PlannerAgent"
    exit 1
}
grep -q "plannerAgent.Start" cmd/runner/cognition.go || {
    echo "❌ runner 未调用 plannerAgent.Start"
    exit 1
}
echo "✅ PlannerAgent 已集成到 runner"

# 6. 检查控制平面路由
echo ""
echo "6. 检查控制平面路由..."
grep -q 'POST.*"/tasks/:id/control"' internal/httpapi/server.go || {
    echo "❌ 控制平面路由未注册"
    exit 1
}
echo "✅ 控制平面路由已注册"

echo ""
echo "=== ✅ 所有集成检查通过 ==="
echo ""
echo "下一步："
echo "1. 启动服务: cd deployments && docker-compose up -d"
echo "2. 运行迁移: make migrate"
echo "3. 发起扫描任务，观察日志中的 PlannerAgent 行为"
echo "4. 使用控制平面 API 手动介入任务"
