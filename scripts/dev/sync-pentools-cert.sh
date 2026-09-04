#!/usr/bin/env bash
# sync-pentools-cert.sh — 把 liusha proxy CA cert 同步到 pentools build context。
#
# 用途：build pentools 镜像前必须跑一次——Dockerfile COPY 这个 cert 到容器，
# update-ca-certificates 让 sandbox 内的 chromium 信任 liusha MITM 代理 CA。
# v34+：CLI 工具直连不经 proxy，CA 仅 chromium 用（passive 入口 8888 仍可用同 CA）。
#
# 前置：liusha proxy 至少启动一次（首次会在 $HOME/.liusha/cacert.pem 生成 CA）。
# 用法：./scripts/dev/sync-pentools-cert.sh
# 之后：docker build -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile.final .

set -euo pipefail
cd "$(dirname "$0")/../.."

CA_SRC="${HOME}/.liusha/cacert.pem"
CA_DST="deployments/tool-images/pentools/liusha-ca.crt"

if [ ! -f "$CA_SRC" ]; then
  echo "✗ $CA_SRC 不存在"
  echo "  请先启动一次 liusha proxy 让它生成 CA：./scripts/dev/run-svc.sh"
  exit 1
fi

cp "$CA_SRC" "$CA_DST"
chmod 644 "$CA_DST"
echo "✓ CA cert 已同步：$CA_SRC → $CA_DST"
echo "  ($(wc -l < "$CA_DST") 行，$(wc -c < "$CA_DST") bytes)"
echo ""
echo "下一步 build pentools 镜像："
echo "  docker build -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile.final ."
