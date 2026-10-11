#!/bin/bash
# 代码质量检查脚本 - 第1轮

echo "===== 第1轮：架构遗留问题检查 ====="
echo ""

echo "1. 检查旧命名残留..."
echo "   - knowledge graph 相关:"
grep -r "knowledge.*graph\|KnowledgeGraph" --include="*.go" --include="*.md" internal/ cmd/ agents/ 2>/dev/null | wc -l

echo "   - 其他旧概念:"
grep -r "oldconcept\|deprecated\|TODO.*rename" --include="*.go" internal/ cmd/ 2>/dev/null | head -5

echo ""
echo "2. 检查注释中的旧概念..."
grep -r "// .*知识图谱\|/\* .*知识图谱" --include="*.go" internal/ cmd/ 2>/dev/null | head -10

echo ""
echo "3. 检查目录结构..."
find internal/ cmd/ -type d | sort

echo ""
echo "4. 检查包命名规范..."
find internal/ -name "*.go" -exec head -1 {} \; | sort | uniq -c | sort -rn | head -20

echo ""
echo "===== 第2轮：代码重复检查 ====="
echo ""

echo "1. 查找重复的函数签名..."
grep -r "^func " --include="*.go" internal/ cmd/ | cut -d: -f2 | sort | uniq -d | head -10

echo ""
echo "2. 查找重复的结构体..."
grep -r "^type.*struct" --include="*.go" internal/ | cut -d: -f2 | sort | uniq -d | head -10

echo ""
echo "===== 第3轮：错误处理检查 ====="
echo ""

echo "1. 未检查的错误..."
grep -r "^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*(" --include="*.go" internal/ cmd/ | grep -v "err" | grep -v "//" | wc -l

echo ""
echo "2. panic 使用..."
grep -rn "panic(" --include="*.go" internal/ cmd/ | wc -l

echo ""
echo "===== 第4轮：日志检查 ====="
echo ""

echo "1. 日志级别分布..."
echo "   DEBUG:" $(grep -r "logger.Debug\|log.Debug" --include="*.go" internal/ cmd/ | wc -l)
echo "   INFO:" $(grep -r "logger.Info\|log.Info" --include="*.go" internal/ cmd/ | wc -l)
echo "   WARN:" $(grep -r "logger.Warn\|log.Warn" --include="*.go" internal/ cmd/ | wc -l)
echo "   ERROR:" $(grep -r "logger.Error\|log.Error" --include="*.go" internal/ cmd/ | wc -l)

echo ""
echo "===== 第5轮：测试覆盖检查 ====="
echo ""

echo "1. 测试文件统计..."
echo "   测试文件数:" $(find internal/ cmd/ -name "*_test.go" | wc -l)
echo "   源代码文件数:" $(find internal/ cmd/ -name "*.go" ! -name "*_test.go" | wc -l)

echo ""
echo "2. 缺少测试的包..."
for dir in $(find internal/ -type d -not -path "*/.*"); do
    if [ -n "$(find "$dir" -maxdepth 1 -name "*.go" ! -name "*_test.go" 2>/dev/null)" ]; then
        if [ -z "$(find "$dir" -maxdepth 1 -name "*_test.go" 2>/dev/null)" ]; then
            echo "   $dir"
        fi
    fi
done

echo ""
echo "===== 检查完成 ====="
