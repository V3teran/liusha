#!/usr/bin/env bash
# install-from-manifest.sh —— 按 tools.yaml 把沙箱工具装齐，逐个实测存在性，缺失即让构建失败。
#
# 单一真相源：tools.yaml 同时定义「agent 看到什么」(name/category/description) 与
# 「构建期怎么装」(install 字段)。本脚本只消费 install，Go 侧只消费 name/category/description，
# 二者互不干扰，install 永不进 user prompt。
#
# ── 判定逻辑 ──────────────────────────────────────────────────────────
#   command -v <name> ?
#   ├─ 在
#   │   ├─ install.force != true → 信任当前已装版本，只 sanity（不装）
#   │   └─ install.force == true → 按 install 装指定版覆盖 → sanity
#   └─ 不在
#       ├─ 有 install.method → 装 → 复查 command -v → sanity
#       └─ 无 install.method → FATAL（工具名错/包名变/漏声明 install）
#   任何 sanity / 复查失败 → FATAL
# 任一工具 FATAL → 脚本非零退出 → docker build 失败（绝不带病出镜像）。
#
# ── tools.yaml 的 install 字段（全部可选；不写=期望上层 Dockerfile 已安装）──
#   install:
#     method: apt|pip|pipx|npm|go|release|release-bin|git
#     pkg:    <包名>                # method=apt/npm 且包名 != name 时
#     ref:    <owner/repo@vX | go module path@vX | git url>
#     asset:  <release-bin 资产名，可含 ${ARCH}/${ARCHX}/${ARCH64}>
#     bin:    <压缩包内二进制名，默认 = name>
#     force:  true                  # 即使已存在也覆盖装
#     check:  '<sanity 命令>'       # 省略则仅以 command -v 为准
#
# 依赖：yq(mikefarah v4)、curl、wget、tar、unzip、git；go/pip/pipx/npm 视 method 出现而定。
set -uo pipefail

MANIFEST="${1:-deployments/tool-images/pentools/tools.yaml}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
TMP="${TMPDIR:-/tmp}/install-from-manifest"

# docker buildx 注入 TARGETARCH；否则探测。amd64/arm64 → 各家 release 命名常用变体。
ARCH="${TARGETARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
case "$ARCH" in
  amd64) ARCHX=x86_64; ARCH64=x64 ;;     # ARCHX: dalfox 等用 x86_64；ARCH64: spectral/node 系用 x64
  arm64) ARCHX=aarch64; ARCH64=arm64 ;;  # ARCHX: aarch64；ARCH64: arm64
  *)     ARCHX="$ARCH"; ARCH64="$ARCH" ;;
esac

fail=0
note() { printf '[install] %s\n' "$*"; }
bad()  { printf '[FATAL] %s\n' "$*" >&2; fail=1; }

# 用 name 查 install.<key>（manifest 体量小，直接 select）；无则空串。
field_by_name() { # <name> <key>
  yq -r "(.tools[] | select(.name == \"$1\") | .install.$2) // \"\"" "$MANIFEST"
}

_dl() { wget -q "$1" -O "$2" && [ -s "$2" ]; }  # 下载且非空

_release_bin() { # <name> <owner/repo@ver> <assetTemplate>
  local name="$1" repo="${2%@*}" ver="${2##*@}" tmpl="$3" asset out
  asset=$(printf '%s' "$tmpl" | sed -e "s/\${ARCH64}/$ARCH64/g" -e "s/\${ARCHX}/$ARCHX/g" -e "s/\${ARCH}/$ARCH/g")
  out="$BIN_DIR/$name"
  _dl "https://github.com/$repo/releases/download/$ver/$asset" "$out" \
    || { bad "$name: 下载失败 $repo $ver $asset"; return 1; }
  chmod +x "$out"
}

_release_archive() { # <name> <owner/repo@ver> <binname>
  local name="$1" repo="${2%@*}" ver="${2##*@}" binname="$3" d="$TMP/$name" f
  rm -rf "$d"; mkdir -p "$d"
  # 依次试常见打包/资产命名：_linux_${ARCH}.tar.gz / -linux-${ARCHX}.tar.gz / _linux_${ARCH}.zip
  for a in "${name}_${ver#v}_linux_${ARCH}.tar.gz" "${name}-${ver}-linux-${ARCHX}.tar.gz" \
           "${name}_${ver#v}_linux_${ARCH}.zip"; do
    if _dl "https://github.com/$repo/releases/download/$ver/$a" "$d/pkg"; then
      case "$a" in *.zip) (cd "$d" && unzip -oq pkg) ;; *) tar -xzf "$d/pkg" -C "$d" ;; esac
      f=$(find "$d" -type f -name "$binname" | head -1)
      [ -n "$f" ] && { cp "$f" "$BIN_DIR/$name"; chmod +x "$BIN_DIR/$name"; rm -rf "$d"; return 0; }
    fi
  done
  bad "$name: release 包内未找到二进制 $binname（$repo $ver）"; rm -rf "$d"; return 1
}

install_one() { # <name>
  local name="$1" method pkg ref asset binname
  method=$(field_by_name "$name" method)
  case "$method" in
    apt)
      pkg=$(field_by_name "$name" pkg); [ -n "$pkg" ] || pkg="$name"
      apt-get install -y --no-install-recommends "$pkg" || { bad "$name: apt 装 $pkg 失败"; return 1; } ;;
    pip)
      ref=$(field_by_name "$name" ref); [ -n "$ref" ] || ref="$name"
      # --ignore-installed：发行版(dpkg)可能已装同名 python 包但无 RECORD 文件，
      # 直接 pip install 会因 uninstall-no-record-file 失败；忽略已装、装到 /usr/local 覆盖。
      pip install --quiet --ignore-installed "$ref" || { bad "$name: pip 装 $ref 失败"; return 1; } ;;
    pipx)
      ref=$(field_by_name "$name" ref); [ -n "$ref" ] || ref="$name"
      pipx install "$ref" || { bad "$name: pipx 装 $ref 失败"; return 1; } ;;
    npm)
      pkg=$(field_by_name "$name" pkg); [ -n "$pkg" ] || pkg="$name"
      npm install -g "$pkg" || { bad "$name: npm 装 $pkg 失败"; return 1; } ;;
    go)
      ref=$(field_by_name "$name" ref); [ -n "$ref" ] || { bad "$name: method=go 缺 ref(module@version)"; return 1; }
      GOBIN="$BIN_DIR" go install "$ref" || { bad "$name: go install $ref 失败"; return 1; } ;;
    release-bin) # 单文件二进制
      ref=$(field_by_name "$name" ref); asset=$(field_by_name "$name" asset)
      [ -n "$ref" ] && [ -n "$asset" ] || { bad "$name: release-bin 需 ref + asset"; return 1; }
      _release_bin "$name" "$ref" "$asset" || return 1 ;;
    release) # 压缩包
      ref=$(field_by_name "$name" ref); binname=$(field_by_name "$name" bin); [ -n "$binname" ] || binname="$name"
      [ -n "$ref" ] || { bad "$name: method=release 缺 ref"; return 1; }
      _release_archive "$name" "$ref" "$binname" || return 1 ;;
    git) # clone 到 /opt/<name>（包装脚本由 Dockerfile 负责，这里只 clone）
      ref=$(field_by_name "$name" ref); [ -n "$ref" ] || { bad "$name: method=git 缺 ref(url)"; return 1; }
      rm -rf "/opt/$name"; git clone --depth 1 "$ref" "/opt/$name" || { bad "$name: git clone $ref 失败"; return 1; } ;;
    "")
      bad "$name: 不在 PATH 且未声明 install.method（核对工具名 / 包名是否变化 / 补 install）"; return 1 ;;
    *)
      bad "$name: 未知 install.method=$method"; return 1 ;;
  esac
}

# 装后 sanity：有 check 跑 check，否则只认 command -v（已在调用处保证存在）。
sanity() { # <name> <check>
  [ -z "$2" ] && return 0
  sh -c "$2" >/dev/null 2>&1 || { bad "$1: sanity 失败（check: $2）"; return 1; }
}

# ── 主循环 ────────────────────────────────────────────────────────────
mkdir -p "$TMP"
total=$(yq -r '.tools | length' "$MANIFEST")
note "manifest=$MANIFEST  工具数=$total  ARCH=$ARCH/$ARCHX"

i=0
while [ "$i" -lt "$total" ]; do
  name=$(yq -r ".tools[$i].name" "$MANIFEST")
  force=$(yq -r ".tools[$i].install.force // \"\"" "$MANIFEST")
  check=$(yq -r ".tools[$i].install.check // \"\"" "$MANIFEST")

  if command -v "$name" >/dev/null 2>&1; then
    if [ "$force" = "true" ]; then
      note "$name: 已存在但 force=true → 覆盖装"
      if install_one "$name" && command -v "$name" >/dev/null 2>&1; then
        sanity "$name" "$check"
      else
        bad "$name: 覆盖装后不可用"
      fi
    else
      note "$name: 已存在（已预装）→ 信任，sanity"
      sanity "$name" "$check"
    fi
  else
    note "$name: 不存在 → 按 install 安装"
    if install_one "$name"; then
      if command -v "$name" >/dev/null 2>&1; then
        sanity "$name" "$check"
      else
        bad "$name: 装完仍不在 PATH"
      fi
    fi
  fi
  i=$((i + 1))
done

rm -rf "$TMP"
if [ "$fail" -ne 0 ]; then
  printf '[install] 存在工具缺失/装配失败，构建终止。\n' >&2
  exit 1
fi
note "全部工具就位 ✅"
