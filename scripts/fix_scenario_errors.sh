#!/bin/bash
# Scenario 深度清理脚本 - 修复编译错误

set -e

echo "🔧 修复关键编译错误..."

# 1. 完全删除 configstore 中的 scenario 相关代码
echo "📦 清理 configstore..."
cat > /tmp/configstore_patch.txt << 'EOF'
删除以下内容：
- scenarioStore 接口定义
- Store.scenarios 字段
- 所有 Scenario* 方法
- keyScenarioCode/keyScenarioID/keyScenariosList 函数
EOF

# 2. 完全删除 config/seed 中的 scenario 导入
echo "📦 清理 config/seed..."
find internal/config/seed -name "*.go" -exec sed -i '' '/importScenarios/d' {} \;
find internal/config/seed -name "*.go" -exec sed -i '' '/cfgscenario/d' {} \;

# 3. 修复 ingestor/traffic.go
echo "📦 修复 ingestor..."
sed -i '' '/ScenarioID.*:/d' internal/ingestor/traffic.go

# 4. 修复 cmd/e2e
echo "📦 修复 e2e..."
find cmd/e2e -name "*.go" -exec sed -i '' 's/ts.List(ctx, "", /ts.List(ctx, /g' {} \;
find cmd/e2e -name "*.go" -exec sed -i '' 's/ts.List(ctx, ".*", /ts.List(ctx, /g' {} \;

# 5. 删除 finding 中的 Scenarios 方法
echo "📦 修复 finding..."
# 需要手动处理

# 6. 删除 cronschedule 中的 scenario_id
echo "📦 修复 cronschedule..."
# 需要手动处理

echo ""
echo "✅ 自动修复完成"
echo "⚠️  以下文件仍需手动处理："
echo "  - internal/configstore/store.go (删除整个 scenario 部分)"
echo "  - internal/config/seed/seed.go (删除 importScenarios)"
echo "  - internal/finding/store.go (删除 Scenarios 方法)"
echo "  - internal/cronschedule/ (删除 scenario_id)"
echo ""
echo "建议：直接删除这些文件中的 scenario 相关方法"
