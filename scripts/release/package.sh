#!/usr/bin/env bash
# scripts/release/package.sh <版本> [输出目录] — 构建发布用的二进制压缩包。
#
# 产物（输出目录缺省 dist/）：
#   liusha_<版本>_linux_amd64.tar.gz / liusha_<版本>_linux_arm64.tar.gz
#     └─ liusha_<版本>_linux_<架构>/{liusha, LICENSE, NOTICE, THIRD_PARTY_NOTICES}
#   checksums.txt（sha256）
#
# 前置：Go、Node 22 + pnpm（web/ 已 pnpm install）。前端先构建并以 webdist 标签编入程序。
set -euo pipefail
cd "$(dirname "$0")/../.."

VERSION="${1:?用法: package.sh <版本> [输出目录]}"
OUT="${2:-dist}"
ARCHES=(amd64 arm64)

case "$OUT" in ''|/|.|..) echo "输出目录不合法: $OUT" >&2; exit 2 ;; esac
rm -rf "${OUT:?}"
mkdir -p "$OUT"
(cd web && pnpm build)

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for arch in "${ARCHES[@]}"; do
  name="liusha_${VERSION}_linux_${arch}"
  mkdir -p "$work/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -mod=vendor -trimpath -tags webdist \
    -ldflags "-s -w -X main.version=${VERSION}" -o "$work/$name/liusha" ./cmd/liusha
  cp LICENSE NOTICE "$work/$name/"
done

# 第三方声明：各架构链接的模块相同，按其中一个生成。
node scripts/release/web-notices.mjs web > "$work/THIRD_PARTY_WEB"
go run -mod=vendor ./scripts/release/notices \
  -binary "$work/liusha_${VERSION}_linux_${ARCHES[0]}/liusha" -web "$work/THIRD_PARTY_WEB" > "$work/THIRD_PARTY_NOTICES"

for arch in "${ARCHES[@]}"; do
  name="liusha_${VERSION}_linux_${arch}"
  cp "$work/THIRD_PARTY_NOTICES" "$work/$name/"
  tar -C "$work" -czf "$OUT/$name.tar.gz" "$name"
done

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
(cd "$OUT" && sha256 ./*.tar.gz | sed 's# \./# #' > checksums.txt)
ls -l "$OUT"
