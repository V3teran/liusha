#!/bin/bash
# liusha lead 包重构验证脚本

set -e

LIUSHA_DIR="/Users/Xlbula/workspace/programs/go/liusha"
cd "$LIUSHA_DIR"

echo "════════════════════════════════════════════════════════════"
echo "  liusha lead 包重构验证"
echo "════════════════════════════════════════════════════════════"
echo ""

FOUND_ISSUE=0

# 1. 编译检查
echo "✅ 步骤 1: 编译检查"
if go build ./... 2>&1 | grep -i "error"; then
  echo "  ✗ 编译失败"
  FOUND_ISSUE=1
else
  echo "  ✓ 编译成功"
fi
echo ""

# 2. 检查新类型是否存在
echo "✅ 步骤 2: 检查新类型定义"

if grep -q "CategoryTarget.*=.*\"target\"" internal/lead/model.go; then
  echo "  ✓ CategoryTarget 已定义"
else
  echo "  ✗ CategoryTarget 未找到"
  FOUND_ISSUE=1
fi

if grep -q "CategoryVulnLead.*=.*\"vuln_lead\"" internal/lead/model.go; then
  echo "  ✓ CategoryVulnLead 已定义"
else
  echo "  ✗ CategoryVulnLead 未找到"
  FOUND_ISSUE=1
fi

if grep -q "PriorityHigh.*=.*\"high\"" internal/lead/model.go; then
  echo "  ✓ PriorityHigh 已定义"
else
  echo "  ✗ PriorityHigh 未找到"
  FOUND_ISSUE=1
fi

if grep -q "ConfidenceTentative.*=.*\"tentative\"" internal/lead/model.go; then
  echo "  ✓ ConfidenceTentative 已定义"
else
  echo "  ✗ ConfidenceTentative 未找到"
  FOUND_ISSUE=1
fi

echo ""

# 3. 检查旧类型是否清理
echo "✅ 步骤 3: 检查旧类型是否清理"

if grep -q "KindClue\|KindHypothesis\|KindDeadend" internal/lead/model.go | grep -v "//"; then
  echo "  ✗ 发现旧类型残留"
  FOUND_ISSUE=1
else
  echo "  ✓ 旧类型已清理"
fi

echo ""

# 4. 检查 Entry 结构体字段
echo "✅ 步骤 4: 检查 Entry 结构体"

if grep -q "Category.*Category" internal/lead/model.go; then
  echo "  ✓ Category 字段存在"
else
  echo "  ✗ Category 字段缺失"
  FOUND_ISSUE=1
fi

if grep -q "Priority.*Priority" internal/lead/model.go; then
  echo "  ✓ Priority 字段存在"
else
  echo "  ✗ Priority 字段缺失"
  FOUND_ISSUE=1
fi

if grep -q "Confidence.*Confidence" internal/lead/model.go; then
  echo "  ✓ Confidence 字段存在"
else
  echo "  ✗ Confidence 字段缺失"
  FOUND_ISSUE=1
fi

if grep -q "Summary.*string" internal/lead/model.go; then
  echo "  ✓ Summary 字段存在"
else
  echo "  ✗ Summary 字段缺失"
  FOUND_ISSUE=1
fi

if grep -q "Tags.*\[\]string" internal/lead/model.go; then
  echo "  ✓ Tags 字段存在"
else
  echo "  ✗ Tags 字段缺失"
  FOUND_ISSUE=1
fi

echo ""

# 5. 检查 migration 文件
echo "✅ 步骤 5: 检查 migration 文件"

if [ -f "db/migrations/0124_refactor_lead_three_dimensions.up.sql" ]; then
  echo "  ✓ Migration 0124 up 文件存在"
else
  echo "  ✗ Migration 0124 up 文件缺失"
  FOUND_ISSUE=1
fi

if [ -f "db/migrations/0124_refactor_lead_three_dimensions.down.sql" ]; then
  echo "  ✓ Migration 0124 down 文件存在"
else
  echo "  ✗ Migration 0124 down 文件缺失"
  FOUND_ISSUE=1
fi

# 检查 migration 内容
if grep -q "category TEXT NOT NULL" db/migrations/0124_refactor_lead_three_dimensions.up.sql; then
  echo "  ✓ Migration 包含 category 字段"
else
  echo "  ✗ Migration 缺少 category 字段"
  FOUND_ISSUE=1
fi

if grep -q "priority TEXT NOT NULL" db/migrations/0124_refactor_lead_three_dimensions.up.sql; then
  echo "  ✓ Migration 包含 priority 字段"
else
  echo "  ✗ Migration 缺少 priority 字段"
  FOUND_ISSUE=1
fi

if grep -q "confidence TEXT NOT NULL" db/migrations/0124_refactor_lead_three_dimensions.up.sql; then
  echo "  ✓ Migration 包含 confidence 字段"
else
  echo "  ✗ Migration 缺少 confidence 字段"
  FOUND_ISSUE=1
fi

echo ""

# 6. 检查工具 schema
echo "✅ 步骤 6: 检查工具 schema"

if grep -q '"category"' internal/tools/lead.go; then
  echo "  ✓ write_lead 工具包含 category 参数"
else
  echo "  ✗ write_lead 工具缺少 category 参数"
  FOUND_ISSUE=1
fi

if grep -q '"priority"' internal/tools/lead.go; then
  echo "  ✓ write_lead 工具包含 priority 参数"
else
  echo "  ✗ write_lead 工具缺少 priority 参数"
  FOUND_ISSUE=1
fi

if grep -q '"confidence"' internal/tools/lead.go; then
  echo "  ✓ write_lead 工具包含 confidence 参数"
else
  echo "  ✗ write_lead 工具缺少 confidence 参数"
  FOUND_ISSUE=1
fi

echo ""

# 7. 检查文档
echo "✅ 步骤 7: 检查文档"

if [ -f "docs/LEAD-FINAL-DESIGN.md" ]; then
  echo "  ✓ LEAD-FINAL-DESIGN.md 存在"
else
  echo "  ✗ LEAD-FINAL-DESIGN.md 缺失"
fi

if [ -f "docs/LEAD-COMPARISON-ANALYSIS.md" ]; then
  echo "  ✓ LEAD-COMPARISON-ANALYSIS.md 存在"
else
  echo "  ✗ LEAD-COMPARISON-ANALYSIS.md 缺失"
fi

if [ -f "docs/LEAD-DIMENSION-ANALYSIS.md" ]; then
  echo "  ✓ LEAD-DIMENSION-ANALYSIS.md 存在"
else
  echo "  ✗ LEAD-DIMENSION-ANALYSIS.md 缺失"
fi

if [ -f "docs/LEAD-REFACTOR-COMPLETE.md" ]; then
  echo "  ✓ LEAD-REFACTOR-COMPLETE.md 存在"
else
  echo "  ✗ LEAD-REFACTOR-COMPLETE.md 缺失"
fi

echo ""

# 8. 统计代码引用
echo "✅ 步骤 8: 统计代码引用"

CATEGORY_COUNT=$(grep -r "CategoryTarget\|CategoryAuth\|CategoryVulnLead" internal/lead --include="*.go" | wc -l | tr -d ' ')
PRIORITY_COUNT=$(grep -r "PriorityHigh\|PriorityMedium\|PriorityLow" internal/lead --include="*.go" | wc -l | tr -d ' ')
CONFIDENCE_COUNT=$(grep -r "ConfidenceTentative\|ConfidenceConfirmed" internal/lead --include="*.go" | wc -l | tr -d ' ')

echo "   引用统计:"
echo "   - Category 常量: $CATEGORY_COUNT 处"
echo "   - Priority 常量: $PRIORITY_COUNT 处"
echo "   - Confidence 常量: $CONFIDENCE_COUNT 处"

echo ""
echo "════════════════════════════════════════════════════════════"

if [ $FOUND_ISSUE -eq 0 ]; then
  echo "✅ lead 包重构验证通过！"
  echo ""
  echo "📋 后续步骤:"
  echo "  1. 执行数据库 migration: migrate -path db/migrations -database \"\$LIUSHA_POSTGRES_DSN\" up"
  echo "  2. 运行测试: go test ./internal/lead -v"
  echo "  3. 端到端验证"
else
  echo "⚠️  发现问题，请检查上述错误"
fi

echo "════════════════════════════════════════════════════════════"

exit $FOUND_ISSUE
