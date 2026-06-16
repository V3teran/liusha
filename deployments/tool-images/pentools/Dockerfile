# syntax=docker/dockerfile:1.7
# liusha pentools 镜像：all-in-one 沙箱 = Kali 全量 CLI 工具集 + browser-use（含 playwright chromium）+
# sandbox-server（Go，容器 PID 1，主进程通过 :8080 HTTP RPC 调用 /exec）。
#
# 设计要点（与 ubuntu 版的关键差异）：
#   - base = kalilinux/kali-rolling + kali-linux-headless 元包：吃 Kali 维护的全量安全工具仓，
#     系统扩展时新增工具大概率"已在镜像里"，只需往 tools.yaml 加一行。
#   - 工具装配走 **manifest 驱动**：scripts/build/install-from-manifest.sh 读 tools.yaml，逐个
#     `command -v` 实测——Kali 已供给则信任跳过，缺了按 install 字段装，任一缺失/装不上即构建失败。
#     版本策略：默认信任 Kali（apt 不 pin）；Kali 没有的（gau/spectral/trufflehog）用 release 装并 pin。
#   - **browser-use 跑在独立 Python 3.12 venv**：Kali rolling 的系统 Python 随 Debian sid 滚到 3.13/3.14，
#     而 browser-use 曾因 3.14 移除 asyncio.get_event_loop() 静默失败（issue #4447）。隔离 3.12 venv
#     只服务 browser-use；其余工具 + agent 现写脚本走 Kali 系统 Python。
#   - multi-stage：Stage 0(golang) 编译 sandbox-server，Stage 1 拷贝；最终镜像不含 Go 工具链。
#   - 构建在 GitHub Actions（US runner）：不再用清华 apt/pip 镜像（US 上更慢）；用默认源。
#   - arm64 优先：原生 arm64 runner 构建；google-chrome 仅 amd64 deb，统一改用 playwright chromium。
#
# 构建（本地）：docker build -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile .
# 构建（CI）：见 .github/workflows/pentools-image.yml（推 GHCR）。
#
# 工具清单的真相源是同目录 tools.yaml（agent 索引 + 构建期 install 双用）。

# ── Stage 0: 编译 sandbox-server（CGO_ENABLED=0 纯 Go，vendored 离线 build）────────────
FROM golang:1.25-alpine AS sandbox-builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/sandbox-server cmd/sandbox-server
COPY internal/sandbox internal/sandbox
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/sandbox-server ./cmd/sandbox-server

# ── Stage 1: Kali 运行镜像 ───────────────────────────────────────────────────────────
FROM kalilinux/kali-rolling

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_RETRIES=5 \
    PIP_TIMEOUT=60 \
    BROWSER_USE_VENV=/opt/browser-use-venv \
    PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# apt 重试（网络抖动不让整层 abort）。
RUN printf 'Acquire::Retries "5";\nAcquire::http::Timeout "120";\nAcquire::https::Timeout "120";\n' \
      > /etc/apt/apt.conf.d/80-retries

# Layer 1: 基础依赖（非工具——工具走 kali-linux-headless + manifest 安装器）。
# wgetrc 全局重试；yq(mikefarah) 给安装器读 tools.yaml 用。
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git unzip xz-utils tar file jq \
        python3 python3-pip python3-venv pipx \
        default-jdk-headless php-cli default-mysql-client \
        build-essential \
 && rm -rf /var/lib/apt/lists/* \
 && printf 'tries=5\nwaitretry=10\ntimeout=60\nretry-connrefused=on\n' >> /etc/wgetrc \
 && wget -q "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${TARGETARCH}" -O /usr/local/bin/yq \
 && chmod +x /usr/local/bin/yq \
 && yq --version

# Layer 2: kali-linux-headless 元包（全量无桌面 CLI 工具集）——最大的一层，单独放便于缓存。
RUN apt-get update \
 && apt-get install -y kali-linux-headless \
 && rm -rf /var/lib/apt/lists/*

# Layer 3: agent 现写脚本常用的 Python 库（装进 Kali 系统 Python——agent 跑 `python3 script.py` 用）。
# 注意：这些是「库」不是「工具」，不进 tools.yaml；与 browser-use 的 venv 互不相干。
RUN pip install --quiet \
        requests httpx aiohttp beautifulsoup4 lxml urllib3 \
        websockets websocket-client pysocks dnspython cryptography \
        pyjwt pyyaml xmltodict paramiko pymysql \
 && python3 -c 'import requests, httpx, aiohttp, bs4, lxml, urllib3, websockets, websocket, socks, dns, cryptography, jwt, yaml, xmltodict, paramiko, pymysql; print("python libs OK")'

# Layer 4: browser-use 专属 Python 3.12 venv（隔离 Kali 滚动 Python）。
# uv 拉 standalone CPython 3.12 建 venv；browser-use[cli] + playwright + httpx 全装进 venv。
# playwright chromium 也由 venv 拉；symlink 到 /usr/local/bin/chromium 给 chromedp 系工具（dalfox/katana）当入口。
# 薄客户端 browser-use 的 shebang 指向 venv 解释器（见该文件首行），它用 sys.executable 拉起
# browser-svc.py → 二者都跑 venv python → import browser_use 命中 venv，不受系统 Python 影响。
RUN pip install --quiet uv \
 && uv venv --python 3.12 "$BROWSER_USE_VENV" \
 && uv pip install --python "$BROWSER_USE_VENV/bin/python" --quiet 'browser-use[cli]' playwright httpx \
 && "$BROWSER_USE_VENV/bin/playwright" install chromium \
 && ("$BROWSER_USE_VENV/bin/playwright" install-deps chromium 2>/dev/null || true) \
 && ln -sf "$BROWSER_USE_VENV/bin/browser-use" /usr/local/bin/browser-use-cli \
 && browser-use-cli --version 2>&1 | head -1 \
 # symlink playwright chromium binary → /usr/local/bin/chromium（arm64 可能是 headless_shell）
 && PW_CHROMIUM=$(find /root/.cache/ms-playwright -type f \( -name chrome -o -name headless_shell \) 2>/dev/null | head -1) \
 && [ -n "$PW_CHROMIUM" ] || { echo "[FATAL] playwright chromium binary 未找到"; ls -la /root/.cache/ms-playwright/ 2>&1; exit 1; } \
 && ln -sf "$PW_CHROMIUM" /usr/local/bin/chromium \
 && /usr/local/bin/chromium --version 2>&1 | head -1

# Layer 4b: browser-use 薄客户端 + 每身份常驻 Page 路由服务（CDP Network capture 内建其中）。
# 薄客户端 shebang = venv 解释器；它 os.execvp("browser-use-cli") 透传管理类子命令（已 symlink 到 venv）。
COPY deployments/tool-images/pentools/browser-use /usr/local/bin/browser-use
COPY deployments/tool-images/pentools/browser-svc.py /usr/local/bin/browser-svc.py
RUN chmod +x /usr/local/bin/browser-use /usr/local/bin/browser-svc.py \
 && head -1 /usr/local/bin/browser-use \
 && /usr/local/bin/browser-use --help >/dev/null 2>&1 || true

# Layer 4c: 特殊装法工具（不适配通用 manifest 安装器）——在安装器之前装好，安装器 command -v 命中即信任跳过。
#   - jwt_tool：git clone + pip 依赖 + python wrapper（PyPI 的 pyjwt 是另一个包）
#   - dalfox：hahwul release，archive 内是带版本名的单文件（dalfox-v$v-linux-$DARCH）
#   - ysoserial：jar + java wrapper
# gh-latest：跟 GitHub /releases/latest 的 302 拿最新 tag（去前导 v）；3 retry；可传 fallback 兜底。
RUN cat <<'EOF' > /usr/local/bin/gh-latest
#!/bin/sh
fallback="$2"
for attempt in 1 2 3; do
  url=$(curl -sILf --max-time 15 -o /dev/null -w '%{url_effective}' "https://github.com/$1/releases/latest" 2>/dev/null) || url=""
  case "$url" in
    *tag/v*) echo "$url" | sed 's|.*tag/v||'; exit 0 ;;
    *tag/*)  echo "$url" | sed 's|.*tag/||';  exit 0 ;;
  esac
  sleep 2
done
[ -n "$fallback" ] && { echo "$fallback"; exit 0; }
exit 1
EOF
RUN set -eu \
 && chmod +x /usr/local/bin/gh-latest \
 # jwt_tool
 && git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && pip install --quiet --ignore-installed -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool \
 && jwt_tool -h 2>&1 | head -1 \
 # dalfox（DARCH: arm64→aarch64 / amd64→x86_64；archive 内单文件名 = dalfox-v$v-linux-$DARCH）
 && v=$(gh-latest hahwul/dalfox 3.0.0) \
 && case "${TARGETARCH}" in arm64) DARCH=aarch64 ;; amd64) DARCH=x86_64 ;; *) DARCH=${TARGETARCH} ;; esac \
 && url="https://github.com/hahwul/dalfox/releases/download/v$v/dalfox-v$v-linux-${DARCH}.tar.gz" \
 && wget "$url" -O /tmp/dalfox.tar.gz \
 && [ -s /tmp/dalfox.tar.gz ] || { echo "[FATAL] dalfox download failed: $url"; exit 1; } \
 && tar -xzf /tmp/dalfox.tar.gz "dalfox-v$v-linux-${DARCH}" -O > /usr/local/bin/dalfox \
 && [ -s /usr/local/bin/dalfox ] || { echo "[FATAL] dalfox binary empty"; exit 1; } \
 && chmod +x /usr/local/bin/dalfox && rm -f /tmp/dalfox.tar.gz \
 && dalfox version 2>&1 | head -1 \
 # ysoserial（jar + wrapper；fallback 0.0.6，多年未变）
 && v=$(gh-latest frohoff/ysoserial 0.0.6) \
 && wget "https://github.com/frohoff/ysoserial/releases/download/v$v/ysoserial-all.jar" -O /opt/ysoserial.jar \
 && [ -s /opt/ysoserial.jar ] || { echo "[FATAL] ysoserial jar 空"; exit 1; } \
 && printf '#!/bin/sh\nexec java -jar /opt/ysoserial.jar "$@"\n' > /usr/local/bin/ysoserial \
 && chmod +x /usr/local/bin/ysoserial \
 && ysoserial 2>&1 | head -1

# Layer 5: manifest 驱动装齐工具——逐个 command -v 实测，Kali 已供给则信任，缺了按 install 装，
# 任一缺失/装不上即构建失败。browser-use 此时已存在（Layer 4），安装器遍历到它会信任跳过。
COPY deployments/tool-images/pentools/tools.yaml /tmp/tools.yaml
COPY scripts/build/install-from-manifest.sh /usr/local/bin/install-from-manifest.sh
RUN apt-get update \
 && chmod +x /usr/local/bin/install-from-manifest.sh \
 && TARGETARCH="${TARGETARCH}" /usr/local/bin/install-from-manifest.sh /tmp/tools.yaml \
 && rm -rf /var/lib/apt/lists/*

# Layer 6: PD 工具 -duc wrapper——拦 httpx/katana/nuclei/subfinder 的 phone-home(api.pdtm.sh 版本检查)。
# 工具可能来自 Kali apt(/usr/bin) 或 release(/usr/local/bin)，用 command -v 定位真路径再包装；
# wrapper 一律落 /usr/local/bin（PATH 在 /usr/bin 之前，自动遮蔽）。
RUN set -eu; for tool in httpx katana nuclei subfinder; do \
      real=$(command -v "$tool") || { echo "[FATAL] $tool 不存在，无法加 -duc wrapper"; exit 1; }; \
      if [ "$real" = "/usr/local/bin/$tool" ]; then mv "$real" "/usr/local/bin/$tool.real"; real="/usr/local/bin/$tool.real"; fi; \
      printf '#!/bin/sh\nexec %s -duc "$@"\n' "$real" > "/usr/local/bin/$tool"; \
      chmod +x "/usr/local/bin/$tool"; \
    done \
 && /usr/local/bin/httpx -version 2>&1 | head -1 \
 && /usr/local/bin/nuclei -version 2>&1 | head -1

# Layer 7: nuclei 模板预拉——Kali 不含模板，nuclei 首次运行才联网拉。
# 用 git clone（比 nuclei 内部 fetcher 可靠，根治"templates: 0 dirs"）到 nuclei 默认模板目录。
# 失败不阻塞 build（运行时还能拉）。
RUN git clone --depth 1 https://github.com/projectdiscovery/nuclei-templates.git \
        /root/.config/nuclei-templates 2>/dev/null \
 && echo "nuclei-templates dirs: $(ls /root/.config/nuclei-templates 2>/dev/null | wc -l)" \
 || echo "[warn] nuclei-templates clone 失败，运行时再拉"

# Layer 8: SecLists 占位（运行时按需 git clone；nuclei/dalfox 内置 payload 已覆盖多数场景）。
RUN mkdir -p /opt/SecLists \
 && printf 'SecLists 未预装；按需拉:\n  git clone --depth=1 https://github.com/danielmiessler/SecLists.git /opt/SecLists\n' \
        > /opt/SecLists/README.md

# 注：旧 ubuntu 版有一层「liusha proxy CA」(liusha-ca.crt) 装进 OpenSSL + NSS。已删除——
# 沙箱 active capture 实际走本地 mitmproxy(Layer 9 的自生成 CA，见 launcher HTTP_PROXY=127.0.0.1:8889)，
# CLI 工具信任的是 mitmproxy CA、不是 liusha CA；chromium 走 CDP 抓包不经代理。liusha CA 在沙箱内已无消费者，
# 且它不入 git(每部署各异) 会卡 CI。故移除：CI 构建不再依赖部署私有文件。

# Layer 9: mitmproxy——容器内捕获 CLI 工具流量入 http_flow 字典（经 HTTP_PROXY → mitmdump → addon → ingest）。
# mitmproxy 用自己的 CA MITM（build 期生成装入 OpenSSL trust），CLI 工具才不拒 HTTPS。
COPY deployments/tool-images/pentools/mitm-capture.py /usr/local/bin/mitm-capture.py
RUN pip install --quiet --ignore-installed mitmproxy \
 && timeout 8 mitmdump --listen-port 18999 -q >/dev/null 2>&1 || true \
 && test -f /root/.mitmproxy/mitmproxy-ca-cert.pem \
 && cp /root/.mitmproxy/mitmproxy-ca-cert.pem /usr/local/share/ca-certificates/mitmproxy-ca.crt \
 && update-ca-certificates \
 && chmod +x /usr/local/bin/mitm-capture.py \
 && echo "[Layer 9] mitmproxy + CA 就位"

# entrypoint：注入 LIUSHA_INGEST_URL 时后台拉起 mitmdump，再 exec sandbox-server 作 PID 1。
RUN printf '%s\n' \
  '#!/bin/sh' \
  'if [ -n "${LIUSHA_INGEST_URL:-}" ]; then' \
  '  mitmdump -q --listen-host 127.0.0.1 --listen-port 8889 \' \
  '    --set block_global=false --set connection_strategy=lazy \' \
  '    -s /usr/local/bin/mitm-capture.py >/tmp/mitm.log 2>&1 &' \
  'fi' \
  'exec /usr/local/bin/sandbox-server' \
  > /usr/local/bin/sandbox-entrypoint.sh \
 && chmod +x /usr/local/bin/sandbox-entrypoint.sh

# Layer 10: sandbox-server 二进制（容器 PID 1）。
COPY --from=sandbox-builder /out/sandbox-server /usr/local/bin/sandbox-server
RUN chmod +x /usr/local/bin/sandbox-server && test -x /usr/local/bin/sandbox-server

WORKDIR /work

EXPOSE 8080

# entrypoint exec form：SIGTERM 直达 sandbox-server，优雅 shutdown。
CMD ["/usr/local/bin/sandbox-entrypoint.sh"]
