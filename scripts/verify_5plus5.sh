#!/bin/bash
# liusha 5+5 迁移验证脚本
# 验证迁移是否成功

set -e

LIUSHA_DIR="/Users/Xlbula/workspace/programs/go/liusha"
cd "$LIUSHA_DIR"

echo "════════════════════════════════════════════════════════════"
echo "  liusha 5+5 迁移验证"
echo "════════════════════════════════════════════════════════════"
echo ""

# 1. 编译检查
echo "✅ 步骤 1: 编译检查"
if go build ./...; then
  echo "   ✓ 编译成功"
else
  echo "   ✗ 编译失败"
  exit 1
fi
echo ""

# 2. 检查关键类型是否存在
echo "✅ 步骤 2: 检查关键类型定义"
if grep -q "KindAction" internal/worldmodel/model.go; then
  echo "   ✓ KindAction 已定义"
else
  echo "   ✗ KindAction 未找到"
  exit 1
fi

if grep -q "KindHypothesis" internal/worldmodel/model.go; then
  echo "   ✓ KindHypothesis 已定义"
else
  echo "   ✗ KindHypothesis 未找到"
  exit 1
fi

if grep -q "KindFinding" internal/worldmodel/model.go; then
  echo "   ✓ KindFinding 已定义"
else
  echo "   ✗ KindFinding 未找到"
  exit 1
fi

if grep -q "KindEvidence" internal/worldmodel/model.go; then
  echo "   ✓ KindEvidence 已定义"
else
  echo "   ✗ KindEvidence 未找到"
  exit 1
fi

if grep -q "RelGenerates" internal/worldmodel/model.go; then
  echo "   ✓ RelGenerates 已定义"
else
  echo "   ✗ RelGenerates 未找到"
  exit 1
fi

if grep -q "RelConfirms" internal/worldmodel/model.go; then
  echo "   ✓ RelConfirms 已定义"
else
  echo "   ✗ RelConfirms 未找到"
  exit 1
fi
echo ""

# 3. 检查旧类型是否已清理
echo "✅ 步骤 3: 检查旧类型是否清理"
if grep -q "KindMove" internal/worldmodel/model.go; then
  echo "   ✗ KindMove 仍然存在（应该被删除）"
  exit 1
else
  echo "   ✓ KindMove 已清理"
fi

if grep -q "KindObservation" internal/worldmodel/model.go; then
  echo "   ✗ KindObservation 仍然存在（应该被删除）"
  exit 1
else
  echo "   ✓ KindObservation 已清理"
fi

if grep -q "KindDiscovery" internal/worldmodel/model.go; then
  echo "   ✗ KindDiscovery 仍然存在（应该被删除）"
  exit 1
else
  echo "   ✓ KindDiscovery 已清理"
fi

if grep -q "RelProduces" internal/worldmodel/model.go; then
  echo "   ✗ RelProduces 仍然存在（应该被删除）"
  exit 1
else
  echo "   ✓ RelProduces 已清理"
fi
echo ""

# 4. 检查 migration 文件
echo "✅ 步骤 4: 检查 migration 文件"
if [ -f "db/migrations/0123_refactor_to_5plus5_model.up.sql" ]; then
  echo "   ✓ Migration 0123 up 文件存在"
else
  echo "   ✗ Migration 0123 up 文件缺失"
  exit 1
fi

if [ -f "db/migrations/0123_refactor_to_5plus5_model.down.sql" ]; then
  echo "   ✓ Migration 0123 down 文件存在"
else
  echo "   ✗ Migration 0123 down 文件缺失"
  exit 1
fi
echo ""

# 5. 检查文档
echo "✅ 步骤 5: 检查文档"
if [ -f "docs/5PLUS5-MODEL.md" ]; then
  echo "   ✓ 5+5 模型文档存在"
else
  echo "   ✗ 5+5 模型文档缺失"
fi

if [ -f "docs/5PLUS5-REFACTOR-SUMMARY.md" ]; then
  echo "   ✓ 重构总结文档存在"
else
  echo "   ✗ 重构总结文档缺失"
fi
echo ""

# 6. 统计代码变更
echo "✅ 步骤 6: 统计代码变更"
ACTION_COUNT=$(grep -r "KindAction" internal cmd --include="*.go" | wc -l | tr -d ' ')
HYPOTHESIS_COUNT=$(grep -r "KindHypothesis" internal cmd --include="*.go" | wc -l | tr -d ' ')
FINDING_COUNT=$(grep -r "KindFinding" internal cmd --include="*.go" | wc -l | tr -d ' ')
EVIDENCE_COUNT=$(grep -r "KindEvidence" internal cmd --include="*.go" | wc -l | tr -d ' ')

echo "   引用统计:"
echo "   - KindAction: $ACTION_COUNT 处"
echo "   - KindHypothesis: $HYPOTHESIS_COUNT 处"
echo "   - KindFinding: $FINDING_COUNT 处"
echo "   - KindEvidence: $EVIDENCE_COUNT 处"
echo ""

# 完成
echo "════════════════════════════════════════════════════════════"
echo "✅ 5+5 迁移验证通过！"
echo "════════════════════════════════════════════════════════════"
echo ""
echo "📋 后续步骤:"
echo "  1. 执行数据库迁移: make migrate-up"
echo "  2. 运行测试: go test ./internal/worldmodel -v"
echo "  3. 更新剩余文档: ARCHITECTURE.md, DATA-FLOW.md"
echo "  4. 端到端验证"
echo ""
