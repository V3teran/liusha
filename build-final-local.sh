#!/bin/bash
# 本地构建 pentools final 镜像
#
# 使用方法：
#   ./build-final-local.sh
#
# 前提条件：
#   1. CI 已成功构建并推送 pentools-base:latest 到 GHCR
#   2. 已登录 GHCR（如果 base 是私有的）：
#      echo $GITHUB_TOKEN | docker login ghcr.io -u V3teran --password-stdin

set -e

BASE_IMAGE="ghcr.io/v3teran/pentools-base:latest"
FINAL_IMAGE="pentools:local"

echo "========================================="
echo "本地构建 Pentools Final 镜像"
echo "========================================="
echo ""

# 步骤 1: 拉取 base 镜像
echo "步骤 1/3: 拉取 base 镜像（8-10GB，可能需要几分钟）"
echo "镜像: $BASE_IMAGE"
echo ""

if docker pull $BASE_IMAGE --platform linux/amd64; then
    echo "✅ Base 镜像拉取成功"
else
    echo "❌ Base 镜像拉取失败"
    echo ""
    echo "可能原因："
    echo "1. CI 还未构建 base 镜像"
    echo "2. base 镜像是私有的，需要先登录："
    echo "   echo \$GITHUB_TOKEN | docker login ghcr.io -u V3teran --password-stdin"
    exit 1
fi

echo ""
echo "========================================="
echo ""

# 步骤 2: 构建 final 镜像
echo "步骤 2/3: 构建 final 镜像（约 2-3 分钟）"
echo "构建参数: BASE_IMAGE=$BASE_IMAGE"
echo ""

docker buildx build \
    --platform linux/amd64 \
    --build-arg BASE_IMAGE=$BASE_IMAGE \
    -t $FINAL_IMAGE \
    -f deployments/tool-images/pentools/Dockerfile.final \
    --load \
    .

echo ""
echo "✅ Final 镜像构建成功"
echo ""
echo "========================================="
echo ""

# 步骤 3: 验证镜像
echo "步骤 3/3: 验证镜像"
echo ""

if docker run --rm $FINAL_IMAGE echo "✅ Pentools final 镜像运行正常"; then
    echo ""
    echo "========================================="
    echo "🎉 构建完成！"
    echo "========================================="
    echo ""
    echo "镜像名称: $FINAL_IMAGE"
    echo ""
    echo "使用示例："
    echo "  docker run --rm $FINAL_IMAGE /bin/bash"
    echo "  docker run --rm $FINAL_IMAGE nmap --version"
    echo ""
    echo "查看镜像："
    echo "  docker images | grep pentools"
    echo ""
else
    echo "❌ 镜像验证失败"
    exit 1
fi
