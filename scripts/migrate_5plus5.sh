#!/bin/bash
# liusha 5+5 世界模型全面迁移脚本
# 日期: 2026-08-28
# 作者: Claude (Opus 5)

set -e

LIUSHA_DIR="/Users/Xlbula/workspace/programs/go/liusha"
BACKUP_DIR="/tmp/liusha_backup_$(date +%Y%m%d_%H%M%S)"

echo "════════════════════════════════════════════════════════════"
echo "  liusha 5+5 世界模型全面迁移"
echo "════════════════════════════════════════════════════════════"
echo ""

# 步骤 0: 备份
echo "📦 步骤 0: 创建备份..."
mkdir -p "$BACKUP_DIR"
cp -r "$LIUSHA_DIR/internal" "$BACKUP_DIR/"
cp -r "$LIUSHA_DIR/cmd" "$BACKUP_DIR/"
echo "   备份位置: $BACKUP_DIR"
echo ""

# 步骤 1: 更新 Go 代码中的节点类型常量
echo "🔄 步骤 1: 更新节点类型常量..."
find "$LIUSHA_DIR/internal" "$LIUSHA_DIR/cmd" -name "*.go" -type f \
  ! -path "*/vendor/*" ! -name "*.pb.go" -exec sed -i '' \
  -e 's/\bKindMove\b/KindAction/g' \
  -e 's/\bKindObservation\b/KindHypothesis/g' \
  -e 's/\bKindDiscovery\b/KindFinding/g' \
  {} +
echo "   ✓ 常量名称更新完成"
echo ""

# 步骤 2: 更新字符串字面量（SQL 查询、JSON 等）
echo "🔄 步骤 2: 更新字符串字面量..."
find "$LIUSHA_DIR/internal" "$LIUSHA_DIR/cmd" -name "*.go" -type f \
  ! -path "*/vendor/*" ! -name "*.pb.go" -exec sed -i '' \
  -e "s/kind = 'move'/kind = 'action'/g" \
  -e 's/kind = "move"/kind = "action"/g' \
  -e "s/kind IN ('move')/kind IN ('action')/g" \
  -e 's/kind IN ("move")/kind IN ("action")/g' \
  -e "s/'observation'/'hypothesis'/g" \
  -e 's/"observation"/"hypothesis"/g' \
  -e "s/'discovery'/'finding'/g" \
  -e 's/"discovery"/"finding"/g' \
  {} +
echo "   ✓ 字符串字面量更新完成"
echo ""

# 步骤 3: 更新关系类型
echo "🔄 步骤 3: 更新关系类型..."
find "$LIUSHA_DIR/internal" "$LIUSHA_DIR/cmd" -name "*.go" -type f \
  ! -path "*/vendor/*" ! -name "*.pb.go" -exec sed -i '' \
  -e 's/\bRelProduces\b/RelGenerates/g' \
  -e 's/\bRelSupports\b/RelConfirms/g' \
  -e 's/\bRelRefutes\b/RelRefutes/g' \
  -e 's/\bRelEnables\b/RelEnables/g' \
  -e 's/\bRelDerives\b/RelDependsOn/g' \
  -e "s/'produces'/'GENERATES'/g" \
  -e 's/"produces"/"GENERATES"/g' \
  -e "s/'supports'/'CONFIRMS'/g" \
  -e 's/"supports"/"CONFIRMS"/g' \
  -e "s/'refutes'/'REFUTES'/g" \
  -e 's/"refutes"/"REFUTES"/g' \
  -e "s/'enables'/'ENABLES'/g" \
  -e 's/"enables"/"ENABLES"/g' \
  {} +
echo "   ✓ 关系类型更新完成"
echo ""

# 步骤 4: 更新方法名
echo "🔄 步骤 4: 更新方法名..."
find "$LIUSHA_DIR/internal" "$LIUSHA_DIR/cmd" -name "*.go" -type f \
  ! -path "*/vendor/*" ! -name "*.pb.go" -exec sed -i '' \
  -e 's/\bListOpenMoves\b/ListOpenActions/g' \
  -e 's/\bListCompletedMoves\b/ListCompletedActions/g' \
  -e 's/\bListMovesByState\b/ListActionsByState/g' \
  -e 's/\bUpdateMoveState\b/UpdateActionState/g' \
  -e 's/\bListDiscoveries\b/ListFindings/g' \
  -e 's/\bListVerifiedDiscoveries\b/ListVerifiedFindings/g' \
  -e 's/\bListObservations\b/ListHypotheses/g' \
  -e 's/\bListUnverifiedObservations\b/ListUnverifiedHypotheses/g' \
  -e 's/\bprocessPendingMoves\b/processPendingActions/g' \
  -e 's/\bexecuteMove\b/executeAction/g' \
  -e 's/\bgetCompletedMoveIDs\b/getCompletedActionIDs/g' \
  -e 's/\bcountPendingMoves\b/countPendingActions/g' \
  {} +
echo "   ✓ 方法名更新完成"
echo ""

# 步骤 5: 更新变量名（仅更新明确的节点类型变量）
echo "🔄 步骤 5: 更新变量名..."
find "$LIUSHA_DIR/internal" "$LIUSHA_DIR/cmd" -name "*.go" -type f \
  ! -path "*/vendor/*" ! -name "*.pb.go" -exec sed -i '' \
  -e 's/\bmoves\s*\[\]/actions []/g' \
  -e 's/\bmoves\s*:=/actions :=/g' \
  -e 's/\bobservations\s*:=/hypotheses :=/g' \
  -e 's/\bdiscoveries\s*:=/findings :=/g' \
  {} +
echo "   ✓ 变量名更新完成"
echo ""

# 步骤 6: 编译检查
echo "🔨 步骤 6: 编译检查..."
cd "$LIUSHA_DIR"
if go build ./...; then
  echo "   ✓ 编译成功"
else
  echo "   ✗ 编译失败，请检查错误"
  echo "   备份位置: $BACKUP_DIR"
  exit 1
fi
echo ""

# 步骤 7: 执行数据库迁移
echo "🗄️  步骤 7: 执行数据库迁移..."
if [ -z "$LIUSHA_POSTGRES_DSN" ]; then
  echo "   ⚠️  环境变量 LIUSHA_POSTGRES_DSN 未设置"
  echo "   请手动执行: migrate -path db/migrations -database \"\$LIUSHA_POSTGRES_DSN\" up"
else
  if command -v migrate &> /dev/null; then
    migrate -path db/migrations -database "$LIUSHA_POSTGRES_DSN" up
    echo "   ✓ 数据库迁移成功"
  else
    echo "   ⚠️  migrate 命令未找到"
    echo "   请手动执行: migrate -path db/migrations -database \"\$LIUSHA_POSTGRES_DSN\" up"
  fi
fi
echo ""

# 步骤 8: 测试（可选）
echo "🧪 步骤 8: 运行测试..."
if go test ./internal/worldmodel -v; then
  echo "   ✓ worldmodel 包测试通过"
else
  echo "   ⚠️  测试失败，请检查"
fi
echo ""

# 完成
echo "════════════════════════════════════════════════════════════"
echo "✅ 5+5 迁移完成！"
echo "════════════════════════════════════════════════════════════"
echo ""
echo "📋 后续步骤:"
echo "  1. 检查编译警告和错误"
echo "  2. 运行完整测试套件: go test ./..."
echo "  3. 更新文档: docs/ARCHITECTURE.md, docs/DATA-FLOW.md"
echo "  4. 端到端验证"
echo ""
echo "📦 备份位置: $BACKUP_DIR"
echo ""
