#!/bin/bash
# 老架构清理验证脚本

echo "🔍 开始老架构残留检查..."
echo ""

# 检查项计数
TOTAL=0
PASSED=0
FAILED=0

# 检查函数
check() {
    local name="$1"
    local cmd="$2"
    local expect_empty="$3"

    TOTAL=$((TOTAL + 1))
    echo "[$TOTAL] 检查: $name"

    result=$(eval "$cmd" 2>/dev/null)

    if [ -z "$result" ]; then
        if [ "$expect_empty" = "true" ]; then
            echo "  ✅ 通过 - 无残留"
            PASSED=$((PASSED + 1))
        else
            echo "  ❌ 失败 - 未找到预期内容"
            FAILED=$((FAILED + 1))
        fi
    else
        if [ "$expect_empty" = "true" ]; then
            echo "  ❌ 失败 - 发现残留:"
            echo "$result" | head -3
            FAILED=$((FAILED + 1))
        else
            echo "  ✅ 通过"
            PASSED=$((PASSED + 1))
        fi
    fi
    echo ""
}

# 1. 检查 worldmodel 包引用
check "worldmodel 包导入" \
    "grep -r 'import.*worldmodel' internal/ --include='*.go' | grep -v _test.go | grep -v '// 已删除'" \
    "true"

# 2. 检查 WorldModelNode 类型
check "WorldModelNode 类型使用" \
    "grep -rn 'WorldModelNode' internal/ --include='*.go' | grep -v knowledgegraph_adapter.go | grep -v types.go" \
    "true"

# 3. 检查 NewWorldModelAdapter 函数调用
check "NewWorldModelAdapter 函数调用" \
    "grep -rn 'NewWorldModelAdapter' internal/ --include='*.go'" \
    "true"

# 4. 检查 bus.EventStore 使用
check "bus.EventStore 使用" \
    "grep -rn 'bus\.EventStore' internal/ --include='*.go'" \
    "true"

# 5. 检查编译
check "项目编译" \
    "go build ./... 2>&1 | grep -i error" \
    "true"

# 6. 验证新架构文件存在
check "persistence 包存在" \
    "ls internal/framework/persistence/interface.go" \
    "false"

check "GraphStore 接口存在" \
    "ls internal/framework/persistence/graph_store.go" \
    "false"

check "EventStore 接口存在" \
    "ls internal/framework/persistence/event_store.go" \
    "false"

check "MemoryStore 实现存在" \
    "ls internal/framework/persistence/memory/memory_store.go" \
    "false"

# 7. 检查 CAS 方法存在
check "CompareAndSwapState 方法" \
    "grep -n 'CompareAndSwapState' internal/framework/core/graphstore.go" \
    "false"

check "CompareAndSwapActionState 方法" \
    "grep -n 'CompareAndSwapActionState' internal/knowledgegraph/adapter.go" \
    "false"

# 8. 检查文件重命名
check "knowledgegraph_adapter.go 存在" \
    "ls internal/executor/knowledgegraph_adapter.go" \
    "false"

check "worldmodel_adapter.go 不存在" \
    "ls internal/executor/worldmodel_adapter.go 2>&1 | grep 'No such file'" \
    "false"

check "tools/knowledgegraph.go 存在" \
    "ls internal/tools/knowledgegraph.go" \
    "false"

check "tools/worldmodel.go 不存在" \
    "ls internal/tools/worldmodel.go 2>&1 | grep 'No such file'" \
    "false"

# 9. 检查注释更新
check "注释中无 worldmodel 残留" \
    "grep -rn 'worldmodel' internal/executor/ internal/tools/ internal/evaluator/ --include='*.go' | grep -v _test.go | grep -v '知识图谱' | grep -v 'knowledge graph'" \
    "true"

# 10. 检查导入路径正确
check "persistence 导入正确" \
    "grep -rn 'internal/framework/persistence' internal/ --include='*.go' | grep -v _test.go" \
    "false"

echo "================================"
echo "📊 检查完成"
echo "================================"
echo "总计: $TOTAL 项"
echo "通过: $PASSED 项 ✅"
echo "失败: $FAILED 项 ❌"
echo ""

if [ $FAILED -eq 0 ]; then
    echo "🎉 所有检查通过！老架构已完全清除。"
    exit 0
else
    echo "⚠️  发现 $FAILED 项问题，请检查上述输出。"
    exit 1
fi
