# syntax=docker/dockerfile:1.7
# liusha pentools 镜像：web 渗透专用沙箱 = 精选 web 安全工具集 + browser-use（含 playwright chromium）+
# sandbox-server（Go，容器 PID 1，主进程通过 :8080 HTTP RPC 调用 /exec）。
#
# 设计要点：
#   - base = ubuntu:24.04（与 CI runner ubuntu-24.04-arm 同源，glibc/ABI 一致；不用 kali-linux-headless
#     全家桶——那个镜像装了 ~160 个 wifi/AD/取证工具，liusha 是 web 专项，利用率不到 10%，镜像白膨胀 3-4GB）
#   - 所有工具按官方 GitHub README / releases 推荐方式安装，不自行推断
#   - 每个工具的依赖（requirements.txt / apt deps）随该工具层一并装齐，防止假阳性
#   - PD 系工具（httpx/nuclei/subfinder/katana/interactsh）用官方 Docker 镜像 COPY --from：
#     绕开 GitHub release 网络抖动（5xx）；官方镜像本身就是 PD 推荐分发方式之一
#   - Python 工具用官方推荐 pipx/pip + 完整 requirements.txt
#   - browser-use 跑在独立 Python 3.12 venv（隔离 ubuntu rolling python）
#   - multi-stage：Stage 0 编译 sandbox-server，Stage 1-5 提取 PD 工具二进制，Stage 6 运行镜像
#   - 全程 linux/arm64（CI runner = ubuntu-24.04-arm，本机 = arm64）
#
# 构建（本地）：docker build -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile .
# 构建（CI）：见 .github/workflows/pentools-image.yml

# ── Stage 0: 编译 sandbox-server ─────────────────────────────────────────────
FROM golang:1.25-alpine AS sandbox-builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/sandbox-server cmd/sandbox-server
COPY internal/sandbox internal/sandbox
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/sandbox-server ./cmd/sandbox-server

# ── Stage 1-5: 从官方镜像提取 PD 系工具二进制 ────────────────────────────────
# 官方镜像保证二进制与平台匹配，完全绕开 GitHub release 下载的网络抖动
FROM projectdiscovery/httpx:v1.9.0 AS pd-httpx
FROM projectdiscovery/nuclei:v3.10.0 AS pd-nuclei
FROM projectdiscovery/subfinder:v2.14.0 AS pd-subfinder
FROM projectdiscovery/katana:v1.6.1 AS pd-katana
FROM projectdiscovery/interactsh:v1.3.1 AS pd-interactsh

# ── Stage 6: ubuntu:24.04 运行镜像 ───────────────────────────────────────────
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_RETRIES=5 \
    PIP_TIMEOUT=60 \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    BROWSER_USE_VENV=/opt/browser-use-venv \
    PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    PIPX_HOME=/opt/pipx \
    PIPX_BIN_DIR=/usr/local/bin

# apt 重试（网络抖动不让整层 abort）
RUN printf 'Acquire::Retries "5";\nAcquire::http::Timeout "120";\nAcquire::https::Timeout "120";\n' \
      > /etc/apt/apt.conf.d/80-retries

# ── Layer 1: 基础依赖 ─────────────────────────────────────────────────────────
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git unzip xz-utils tar file \
        python3 python3-pip python3-venv pipx \
        nodejs npm \
        default-jdk-headless \
        php-cli \
        default-mysql-client \
        build-essential \
        libssl-dev libffi-dev \
 && rm -rf /var/lib/apt/lists/* \
 && printf 'tries=5\nwaitretry=10\ntimeout=60\nretry-connrefused=on\n' >> /etc/wgetrc \
 # yq：manifest 安装器读 tools.yaml 用
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${TARGETARCH}" \
        -O /usr/local/bin/yq \
 && chmod +x /usr/local/bin/yq \
 && yq --version

# ── Layer 2: apt 可直接安装的 web 安全工具 ────────────────────────────────────
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        nmap \
        sqlmap \
        hydra \
        ffuf \
        dirsearch \
        wafw00f \
 && rm -rf /var/lib/apt/lists/*

# ── Layer 3: Python 工具（pipx / pip，官方推荐方式）─────────────────────────
# arjun: 官方推荐 pipx（https://github.com/s0md3v/Arjun）
# semgrep: 官方推荐 pip（https://github.com/semgrep/semgrep，无 arm64 binary）
RUN pipx install arjun \
 && arjun --help > /dev/null 2>&1 \
 && pip install --quiet semgrep \
 && semgrep --version

# ── Layer 4: PD 系工具 COPY --from 官方镜像 ──────────────────────────────────
# 官方镜像是 PD 推荐分发方式之一，绕开 GitHub release 下载网络抖动
# 各镜像二进制路径均为 /usr/local/bin/<tool>
COPY --from=pd-httpx        /usr/local/bin/httpx        /usr/local/bin/httpx
COPY --from=pd-nuclei       /usr/local/bin/nuclei       /usr/local/bin/nuclei
COPY --from=pd-subfinder    /usr/local/bin/subfinder    /usr/local/bin/subfinder
COPY --from=pd-katana       /usr/local/bin/katana       /usr/local/bin/katana
COPY --from=pd-interactsh   /usr/local/bin/interactsh-client /usr/local/bin/interactsh-client
RUN chmod +x /usr/local/bin/httpx /usr/local/bin/nuclei \
        /usr/local/bin/subfinder /usr/local/bin/katana \
        /usr/local/bin/interactsh-client \
 && httpx -version \
 && nuclei -version \
 && subfinder -version \
 && katana -version \
 && interactsh-client -version

# ── Layer 5: PD 工具 -duc wrapper ─────────────────────────────────────────────
# 拦截 httpx/katana/nuclei/subfinder 的 phone-home（api.pdtm.sh 版本检查）
RUN set -eu; for tool in httpx katana nuclei subfinder; do \
      real=$(command -v "$tool") || { echo "[FATAL] $tool 不存在，无法加 -duc wrapper"; exit 1; }; \
      mv "$real" "${real}.real"; \
      printf '#!/bin/sh\nexec %s.real -duc "$@"\n' "$real" > "$real"; \
      chmod +x "$real"; \
    done \
 && /usr/local/bin/httpx -version 2>&1 | head -1 \
 && /usr/local/bin/nuclei -version 2>&1 | head -1

# ── Layer 6: 其他 release binary 工具 ────────────────────────────────────────
# trufflehog: 官方推荐 install.sh（https://github.com/trufflesecurity/trufflehog）
# gau v2.2.4: release tar.gz（https://github.com/lc/gau）
# spectral: 官方推荐 npm install -g（https://github.com/stoplightio/spectral，6.x 无 arm64 binary）
RUN set -eux \
 # trufflehog（官方 install.sh，内置重试）
 && curl -sSfL --retry 3 --retry-delay 5 \
        https://raw.githubusercontent.com/trufflesecurity/trufflehog/main/scripts/install.sh \
        | sh -s -- -b /usr/local/bin \
 && trufflehog --version \
 # gau v2.2.4
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/lc/gau/releases/download/v2.2.4/gau_2.2.4_linux_${TARGETARCH}.tar.gz" \
        -O /tmp/gau.tar.gz \
 && tar -xzf /tmp/gau.tar.gz -C /usr/local/bin gau \
 && chmod +x /usr/local/bin/gau \
 && rm /tmp/gau.tar.gz \
 && gau --version \
 # spectral（官方推荐 npm install -g @stoplight/spectral-cli）
 && npm install -g @stoplight/spectral-cli \
 && spectral --version

# ── Layer 7: 专属安装层（逻辑复杂，不适配 manifest 安装器）─────────────────────
# jwt_tool: git clone + pip install requirements.txt（官方：https://github.com/ticarpi/jwt_tool）
# dalfox v3.1.2: release（官方：https://github.com/hahwul/dalfox）
#   arm64 资产名：dalfox-v{ver}-linux-aarch64.tar.gz（官方命名 aarch64）
# ysoserial v0.0.6: fat jar（官方：https://github.com/frohoff/ysoserial）
# phpggc: git clone + php-cli wrapper（官方：https://github.com/ambionics/phpggc，无 release tag）
RUN set -eux \
 # jwt_tool
 && git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && pip install --quiet -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool \
 && jwt_tool --help 2>&1 | head -2 \
 # dalfox v3.1.2（arm64 资产名 aarch64）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/hahwul/dalfox/releases/download/v3.1.2/dalfox-v3.1.2-linux-aarch64.tar.gz" \
        -O /tmp/dalfox.tar.gz \
 && tar -xzf /tmp/dalfox.tar.gz -C /tmp \
 && mv /tmp/dalfox /usr/local/bin/dalfox \
 && chmod +x /usr/local/bin/dalfox \
 && rm -f /tmp/dalfox.tar.gz \
 && dalfox version 2>&1 | head -1 \
 # ysoserial v0.0.6（fat jar，架构无关）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/frohoff/ysoserial/releases/download/v0.0.6/ysoserial-all.jar" \
        -O /opt/ysoserial.jar \
 && [ -s /opt/ysoserial.jar ] || { echo "[FATAL] ysoserial jar 下载失败"; exit 1; } \
 && printf '#!/bin/sh\nexec java -jar /opt/ysoserial.jar "$@"\n' > /usr/local/bin/ysoserial \
 && chmod +x /usr/local/bin/ysoserial \
 && ysoserial 2>&1 | head -2 \
 # phpggc（git clone，无 release tag；需 php-cli）
 && git clone --depth 1 https://github.com/ambionics/phpggc.git /opt/phpggc \
 && printf '#!/bin/sh\nexec php /opt/phpggc/phpggc "$@"\n' > /usr/local/bin/phpggc \
 && chmod +x /usr/local/bin/phpggc \
 && phpggc -l 2>&1 | head -3

# ── Layer 8: manifest 驱动安装器（验证所有工具真实可用）──────────────────────
COPY deployments/tool-images/pentools/tools.yaml /tmp/tools.yaml
COPY scripts/build/install-from-manifest.sh /usr/local/bin/install-from-manifest.sh
RUN apt-get update \
 && chmod +x /usr/local/bin/install-from-manifest.sh \
 && TARGETARCH="${TARGETARCH}" /usr/local/bin/install-from-manifest.sh /tmp/tools.yaml \
 && rm -rf /var/lib/apt/lists/*

# ── Layer 9: nuclei 模板预拉 ──────────────────────────────────────────────────
# git clone 等价于官方推荐的 nuclei -ut，在 CI 里比 nuclei 内部 fetcher 更可靠
RUN git clone --depth 1 https://github.com/projectdiscovery/nuclei-templates.git \
        /root/nuclei-templates 2>/dev/null \
 && echo "nuclei-templates: $(ls /root/nuclei-templates | wc -l) dirs" \
 || echo "[warn] nuclei-templates clone 失败，运行时 nuclei -ut 再拉"

# ── Layer 10: browser-use 专属 Python 3.12 venv ───────────────────────────────
RUN pip install --quiet uv \
 && uv venv --python 3.12 "$BROWSER_USE_VENV" \
 && uv pip install --python "$BROWSER_USE_VENV/bin/python" --quiet 'browser-use[cli]' playwright httpx \
 && "$BROWSER_USE_VENV/bin/playwright" install chromium \
 && ("$BROWSER_USE_VENV/bin/playwright" install-deps chromium 2>/dev/null || true) \
 && ln -sf "$BROWSER_USE_VENV/bin/browser-use" /usr/local/bin/browser-use-cli \
 && browser-use-cli --version 2>&1 | head -1 \
 && PW_CHROMIUM=$(find /root/.cache/ms-playwright -type f \( -name chrome -o -name headless_shell \) 2>/dev/null | head -1) \
 && [ -n "$PW_CHROMIUM" ] || { echo "[FATAL] playwright chromium binary 未找到"; exit 1; } \
 && ln -sf "$PW_CHROMIUM" /usr/local/bin/chromium \
 && /usr/local/bin/chromium --version 2>&1 | head -1

# ── Layer 10b: browser-use 薄客户端 + browser-svc.py ─────────────────────────
COPY deployments/tool-images/pentools/browser-use /usr/local/bin/browser-use
COPY deployments/tool-images/pentools/browser-svc.py /usr/local/bin/browser-svc.py
RUN chmod +x /usr/local/bin/browser-use /usr/local/bin/browser-svc.py \
 && head -1 /usr/local/bin/browser-use \
 && /usr/local/bin/browser-use --help >/dev/null 2>&1 || true

# ── Layer 11: agent 现写脚本常用 Python 库 ────────────────────────────────────
RUN pip install --quiet \
        requests httpx aiohttp beautifulsoup4 lxml urllib3 \
        websockets websocket-client pysocks dnspython cryptography \
        pyjwt pyyaml xmltodict paramiko pymysql \
 && python3 -c 'import requests, httpx, aiohttp, bs4, lxml, urllib3, websockets, websocket, socks, dns, cryptography, jwt, yaml, xmltodict, paramiko, pymysql; print("python libs OK")'

# ── Layer 12: mitmproxy ───────────────────────────────────────────────────────
COPY deployments/tool-images/pentools/mitm-capture.py /usr/local/bin/mitm-capture.py
RUN pip install --quiet --ignore-installed mitmproxy \
 && timeout 8 mitmdump --listen-port 18999 -q >/dev/null 2>&1 || true \
 && test -f /root/.mitmproxy/mitmproxy-ca-cert.pem \
 && cp /root/.mitmproxy/mitmproxy-ca-cert.pem /usr/local/share/ca-certificates/mitmproxy-ca.crt \
 && update-ca-certificates \
 && chmod +x /usr/local/bin/mitm-capture.py \
 && echo "[Layer 12] mitmproxy + CA 就位"

# ── Layer 13: entrypoint ──────────────────────────────────────────────────────
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

# ── Layer 14: sandbox-server 二进制（容器 PID 1）─────────────────────────────
COPY --from=sandbox-builder /out/sandbox-server /usr/local/bin/sandbox-server
RUN chmod +x /usr/local/bin/sandbox-server && test -x /usr/local/bin/sandbox-server

WORKDIR /work
EXPOSE 8080
CMD ["/usr/local/bin/sandbox-entrypoint.sh"]

