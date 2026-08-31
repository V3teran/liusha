#!/bin/bash
# Scenario 概念批量清理脚本
# 警告：此脚本会修改大量文件，运行前请确保代码已提交

set -e

echo "🚀 开始批量清理 scenario 概念..."

# 1. 批量删除 import 引用
echo "📦 删除 scenario 包的 import..."
find internal cmd -name "*.go" -type f -exec sed -i '' '/cfgscenario "github.com\/V3teran\/liusha\/internal\/config\/scenario"/d' {} \;

# 2. 批量替换注释中的 scenario 引用
echo "📝 清理注释中的 scenario 引用..."
find internal cmd -name "*.go" -type f -exec sed -i '' 's/scenario\/agent/agent/g' {} \;
find internal cmd -name "*.go" -type f -exec sed -i '' 's/scenario\/executor/executor/g' {} \;

# 3. 清理测试文件中的 scenario 引用
echo "🧪 清理测试文件..."
find internal -name "*_test.go" -type f -exec grep -l "scenario" {} \; | while read file; do
    echo "  处理: $file"
    # 删除 scenarioID 参数
    sed -i '' 's/, ""[[:space:]]*\/\/ scenarioID//g' "$file"
    sed -i '' 's/scenarioID[[:space:]]*:=.*//g' "$file"
    sed -i '' 's/ScenarioID[[:space:]]*string//g' "$file"
done

# 4. 统计剩余引用
echo ""
echo "📊 剩余 scenario 引用统计："
echo "  Go 文件: $(grep -r "scenario" internal cmd --include="*.go" 2>/dev/null | wc -l) 处"
echo "  SQL 文件: $(grep -r "scenario" db/migrations --include="*.sql" 2>/dev/null | wc -l) 处"
echo ""

echo "✅ 批量清理完成！"
echo "⚠️  请运行以下命令检查编译："
echo "    go build ./..."
echo "    go test ./..."
