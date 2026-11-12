#!/usr/bin/env bash
# scripts/release/package.sh <版本> [输出目录] — 构建发布用的二进制压缩包。
#
# 产物（输出目录缺省 dist/）：
#   liusha_<版本>_linux_{amd64,arm64}.tar.gz
#     └─ liusha_<版本>_linux_<架构>/{liusha, LICENSE, NOTICE, THIRD_PARTY_NOTICES}
#   liusha_<版本>_windows_{amd64,arm64}.zip
#     └─ liusha_<版本>_windows_<架构>/{liusha.exe, LICENSE, NOTICE, THIRD_PARTY_NOTICES}
#   checksums.txt（sha256，覆盖全部压缩包）
#
# 前置：Go、Node 22 + pnpm（web/ 已 pnpm install）、zip。前端先构建并以 webdist 标签编入程序。
set -euo pipefail
cd "$(dirname "$0")/../.."

VERSION="${1:?用法: package.sh <版本> [输出目录]}"
OUT="${2:-dist}"
TARGETS=(linux/amd64 linux/arm64 windows/amd64 windows/arm64)

case "$OUT" in ''|/|.|..) echo "输出目录不合法: $OUT" >&2; exit 2 ;; esac
command -v zip >/dev/null || { echo "需要 zip" >&2; exit 2; }
rm -rf "${OUT:?}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
(cd web && pnpm build)

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
node scripts/release/web-notices.mjs web > "$work/THIRD_PARTY_WEB"

for target in "${TARGETS[@]}"; do
  os="${target%/*}" arch="${target#*/}"
  name="liusha_${VERSION}_${os}_${arch}"
  exe="liusha"; [ "$os" = windows ] && exe="liusha.exe"
  mkdir -p "$work/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -mod=vendor -trimpath -tags webdist \
    -ldflags "-s -w -X main.version=${VERSION}" -o "$work/$name/$exe" ./cmd/liusha
  cp LICENSE NOTICE "$work/$name/"
  # 第三方声明按程序实际链接的模块生成：各平台链接的模块不同（如 Windows 的服务与作业对象支持）。
  go run -mod=vendor ./scripts/release/notices \
    -binary "$work/$name/$exe" -web "$work/THIRD_PARTY_WEB" > "$work/$name/THIRD_PARTY_NOTICES"
  if [ "$os" = windows ]; then
    (cd "$work" && zip -qr "$OUT/$name.zip" "$name")
  else
    tar -C "$work" -czf "$OUT/$name.tar.gz" "$name"
  fi
done

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
(cd "$OUT" && sha256 ./*.tar.gz ./*.zip | sed 's# \./# #' > checksums.txt)
ls -l "$OUT"
