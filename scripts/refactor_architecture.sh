#!/bin/bash
# Liusha Agent 架构优化重构脚本
# 执行以下 4 个调整：
# 1. Lead → Insight
# 2. Verifier → Evaluator
# 3. Move → Action 统一
# 4. Complexity 注释优化

set -e

echo "🚀 开始 Liusha Agent 架构优化重构..."
echo ""

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# ============================================================
# 第 1 步：Lead → Insight
# ============================================================
echo -e "${GREEN}[1/4] 重命名 Lead → Insight${NC}"

# 1.1 重命名包目录
if [ -d "internal/lead" ]; then
    echo "  - 重命名目录: internal/lead → internal/insight"
    git mv internal/lead internal/insight
fi

# 1.2 重命名文件内容
echo "  - 更新文件内容中的 Lead → Insight"
find . -name "*.go" -type f -exec sed -i '' \
    -e 's/package lead/package insight/g' \
    -e 's/import.*".*\/lead"/import "github.com\/V3teran\/liusha\/internal\/insight"/g' \
    -e 's/lead\./insight./g' \
    -e 's/Lead/Insight/g' \
    -e 's/情报/洞察/g' \
    {} \;

# 1.3 更新 SQL migration
find db/migrations -name "*.sql" -type f -exec sed -i '' \
    -e 's/lead /insight /g' \
    -e 's/TABLE lead/TABLE insight/g' \
    {} \;

echo -e "${GREEN}  ✓ Lead → Insight 完成${NC}"
echo ""

# ============================================================
# 第 2 步：Verifier → Evaluator
# ============================================================
echo -e "${GREEN}[2/4] 重命名 Verifier → Evaluator${NC}"

# 2.1 重命名包目录
if [ -d "internal/verifier" ]; then
    echo "  - 重命名目录: internal/verifier → internal/evaluator"
    git mv internal/verifier internal/evaluator
fi

# 2.2 重命名文件内容
echo "  - 更新文件内容中的 Verifier → Evaluator"
find . -name "*.go" -type f -exec sed -i '' \
    -e 's/package verifier/package evaluator/g' \
    -e 's/import.*".*\/verifier"/import "github.com\/V3teran\/liusha\/internal\/evaluator"/g' \
    -e 's/verifier\./evaluator./g' \
    -e 's/Verifier/Evaluator/g' \
    -e 's/验证器/评估器/g' \
    {} \;

echo -e "${GREEN}  ✓ Verifier → Evaluator 完成${NC}"
echo ""

# ============================================================
# 第 3 步：Move → Action 统一
# ============================================================
echo -e "${GREEN}[3/4] 统一 Move → Action${NC}"

# 3.1 更新 Planner 工具
echo "  - 更新 Planner 工具命名"
find internal/planner -name "*.go" -type f -exec sed -i '' \
    -e 's/ProposeMovesTool/ProposeActionsTool/g' \
    -e 's/propose_moves/propose_actions/g' \
    -e 's/生成新的 Move/生成新的 Action/g' \
    -e 's/Move /Action /g' \
    {} \;

# 3.2 更新注释中的 Move
find . -name "*.go" -type f -exec sed -i '' \
    -e 's/\/\/ Move /\/\/ Action /g' \
    -e 's/Move：/Action：/g' \
    {} \;

echo -e "${GREEN}  ✓ Move → Action 统一完成${NC}"
echo ""

# ============================================================
# 第 4 步：Complexity 注释优化
# ============================================================
echo -e "${GREEN}[4/4] 优化 Complexity 注释${NC}"

# 查找 Complexity 定义并更新注释
find internal/worldmodel -name "model.go" -type f -exec sed -i '' \
    -e 's/ComplexityTrivial.*\/\/.*/ComplexityTrivial  Complexity = "trivial"  \/\/ 极简任务/g' \
    -e 's/ComplexitySimple.*\/\/.*/ComplexitySimple   Complexity = "simple"   \/\/ 简单任务/g' \
    -e 's/ComplexityModerate.*\/\/.*/ComplexityModerate Complexity = "moderate" \/\/ 中等任务/g' \
    -e 's/ComplexityComplex.*\/\/.*/ComplexityComplex  Complexity = "complex"  \/\/ 复杂任务/g' \
    -e 's/ComplexityExtreme.*\/\/.*/ComplexityExtreme  Complexity = "extreme"  \/\/ 极限任务/g' \
    {} \;

echo -e "${GREEN}  ✓ Complexity 注释优化完成${NC}"
echo ""

# ============================================================
# 完成
# ============================================================
echo -e "${GREEN}✅ 所有重构完成！${NC}"
echo ""
echo "📝 下一步："
echo "  1. 运行测试: go test ./..."
echo "  2. 检查编译: go build ./..."
echo "  3. 提交更改: git add . && git commit -m 'refactor: 架构优化重命名'"
echo ""
echo -e "${YELLOW}⚠️  注意：请手动检查以下文件是否需要调整：${NC}"
echo "  - README.md"
echo "  - docs/*.md"
echo "  - 配置文件"
