# syntax=docker/dockerfile:1.7
# liusha pentools 镜像：web 渗透专用沙箱 = 精选 web 安全工具集 + browser-use + sandbox-server
#
# 设计要点：
#   - base = kalilinux/kali-rolling，但【绝不装 kali-linux-headless 元包】
#     （headless 会拉 ~160 个 wifi/AD/取证工具，web 专项用不到，镜像白膨胀 3-4GB）
#     只 apt install 需要的工具——最大化利用 Kali 维护的安全工具仓（版本新、免下载）
#   - Kali apt 直供的工具走 apt（sqlmap/hydra/ffuf/dirsearch/arjun/wafw00f/nmap/
#     subfinder/httpx-toolkit/nuclei/phpggc/trufflehog）
#   - Kali apt 没有的走官方推荐方式：katana/gau/interactsh(release)、semgrep(pip)、
#     jwt_tool(git+pip)、ysoserial(jar)、ysomap(git+mvn)、dalfox(release musl)、spectral(npm)
#   - httpx 在 Kali 包名是 httpx-toolkit（避与 python3-httpx 冲突），命令也叫 httpx-toolkit，
#     包装成 /usr/local/bin/httpx 给 agent 用
#   - 双 JDK（Temurin）：JDK8(8u352)默认 + JDK17(17.0.19) 现代目标；java8/java17 显式切换。
#     Java 反序列化/RCE payload：默认 JDK8 编译（class v52 向前兼容所有更高目标）。
#     ysomap/ysoserial 包装脚本硬编码 JDK8 运行。
#   - CTF 兼顾：Go(apt) 编 Go payload；Python 加 pwntools(pwn)+pycryptodome(crypto)；
#     php/gcc/g++ 已含（PHP/C payload）。
#   - PEP 668 在 Kali 同样开启，pip 需 --break-system-packages(env) + --ignore-installed
#   - browser-use 跑独立 Python 3.12 venv（隔离 Kali rolling python）
#   - multi-stage：Stage 0 编译 sandbox-server，Stage 1 运行镜像
#   - 全程 linux/arm64（CI runner = ubuntu-24.04-arm）
#
# 工具清单真相源：同目录 tools.yaml（agent 索引 + 构建期 install 双用）

# ── Stage 0: 编译 sandbox-server ─────────────────────────────────────────────
FROM golang:1.25-alpine AS sandbox-builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/sandbox-server cmd/sandbox-server
COPY internal/sandbox internal/sandbox
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/sandbox-server ./cmd/sandbox-server

# ── Stage 1: kali-rolling 运行镜像 ───────────────────────────────────────────
FROM kalilinux/kali-rolling

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_RETRIES=5 \
    PIP_TIMEOUT=60 \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    BROWSER_USE_VENV=/opt/browser-use-venv \
    PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# apt 重试（网络抖动不让整层 abort）
RUN printf 'Acquire::Retries "5";\nAcquire::http::Timeout "120";\nAcquire::https::Timeout "120";\n' \
      > /etc/apt/apt.conf.d/80-retries

# ── Layer 1: 基础依赖 ─────────────────────────────────────────────────────────
# TARGETARCH 是 buildx 预定义平台参数，ARG 激活后转 ENV 才能在所有后续 RUN 层展开
ARG TARGETARCH
ENV TARGETARCH=${TARGETARCH}
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git unzip xz-utils tar file \
        python3 python3-pip python3-venv \
        nodejs npm \
        php-cli \
        golang maven \
        default-mysql-client \
        build-essential libssl-dev libffi-dev \
 && rm -rf /var/lib/apt/lists/* \
 && printf 'tries=5\nwaitretry=10\ntimeout=60\nretry-connrefused=on\n' >> /etc/wgetrc \
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${TARGETARCH}" \
        -O /usr/local/bin/yq \
 && chmod +x /usr/local/bin/yq \
 && yq --version

# ── Layer 1b: 双 JDK（Temurin）——CTF/Java 反序列化利用 ──────────────────────
# Kali apt 无 openjdk-8/17（只有 11/21/25-ea），从 Adoptium Temurin 下 tar.gz。
#   - JDK 8（8u352-b08）→ /opt/jdk8：默认 java/javac 指向它。
#     Java8 编译的 class（v52）向前兼容所有更高 Java 目标，payload 兼容面最广；
#     ysomap 官方也要求 JDK8 环境编译/运行（XStream 部分 payload 依赖）。
#   - JDK 17（17.0.19+10）→ /opt/jdk17：现代目标（Spring Boot 3.x 强制 17+）专项。
# 架构映射：arm64→aarch64，amd64→x64（Temurin 命名）。
# 版本段无连字符：8u352b08 / 17.0.19_10。
RUN set -eux \
 && case "${TARGETARCH}" in \
      arm64) J_ARCH=aarch64 ;; \
      amd64) J_ARCH=x64     ;; \
      *)     echo "[FATAL] 不支持的架构 ${TARGETARCH}"; exit 1 ;; \
    esac \
 # JDK 8u352-b08
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/adoptium/temurin8-binaries/releases/download/jdk8u352-b08/OpenJDK8U-jdk_${J_ARCH}_linux_hotspot_8u352b08.tar.gz" \
        -O /tmp/jdk8.tar.gz \
 && mkdir -p /opt/jdk8 \
 && tar -xzf /tmp/jdk8.tar.gz -C /opt/jdk8 --strip-components=1 \
 && rm /tmp/jdk8.tar.gz \
 && /opt/jdk8/bin/java -version \
 # JDK 17.0.19+10
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.19+10/OpenJDK17U-jdk_${J_ARCH}_linux_hotspot_17.0.19_10.tar.gz" \
        -O /tmp/jdk17.tar.gz \
 && mkdir -p /opt/jdk17 \
 && tar -xzf /tmp/jdk17.tar.gz -C /opt/jdk17 --strip-components=1 \
 && rm /tmp/jdk17.tar.gz \
 && /opt/jdk17/bin/java -version \
 # 版本切换：默认 java/javac → JDK8（兼容面最广）；显式 java8/java17/javac8/javac17
 && for b in java javac keytool; do \
      update-alternatives --install "/usr/bin/$b" "$b" "/opt/jdk8/bin/$b" 800; \
      update-alternatives --install "/usr/bin/$b" "$b" "/opt/jdk17/bin/$b" 170; \
      update-alternatives --set "$b" "/opt/jdk8/bin/$b"; \
    done \
 && printf '#!/bin/sh\nexec /opt/jdk8/bin/java "$@"\n'   > /usr/local/bin/java8 \
 && printf '#!/bin/sh\nexec /opt/jdk8/bin/javac "$@"\n'  > /usr/local/bin/javac8 \
 && printf '#!/bin/sh\nexec /opt/jdk17/bin/java "$@"\n'  > /usr/local/bin/java17 \
 && printf '#!/bin/sh\nexec /opt/jdk17/bin/javac "$@"\n' > /usr/local/bin/javac17 \
 && chmod +x /usr/local/bin/java8 /usr/local/bin/javac8 /usr/local/bin/java17 /usr/local/bin/javac17 \
 && echo "默认 java = $(java -version 2>&1 | head -1)" \
 && echo "java8 = $(java8 -version 2>&1 | head -1)" \
 && echo "java17 = $(java17 -version 2>&1 | head -1)"

# ── Layer 2: Kali apt 直供的 web 安全工具 ────────────────────────────────────
# Kali 维护这些工具的最新版，apt 装免下载、免 pip 冲突。
# httpx 包名是 httpx-toolkit（命令同名，Layer 3 包装成 /usr/local/bin/httpx）。
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        nmap sqlmap hydra ffuf dirsearch wafw00f arjun \
        subfinder httpx-toolkit nuclei \
        phpggc trufflehog \
 && rm -rf /var/lib/apt/lists/*

# ── Layer 3: PD 系工具 -duc wrapper + httpx 命名兼容 ─────────────────────────
# 拦截 httpx/katana/nuclei/subfinder 的 phone-home（api.pdtm.sh 版本检查）。
# httpx：Kali 命令是 httpx-toolkit → 包装成 /usr/local/bin/httpx（agent 调 httpx）。
# nuclei/subfinder：apt 装在 /usr/bin，包装脚本落 /usr/local/bin（PATH 优先，遮蔽原命令）。
# katana 由 Layer 4 release 装（此处不含，Layer 4 自带 -duc 无必要——katana 不 phone-home 版本检查）。
RUN set -eu \
 && printf '#!/bin/sh\nexec httpx-toolkit -duc "$@"\n' > /usr/local/bin/httpx \
 && chmod +x /usr/local/bin/httpx \
 && for tool in nuclei subfinder; do \
      real=$(command -v "$tool") || { echo "[FATAL] $tool 不存在"; exit 1; }; \
      printf '#!/bin/sh\nexec %s -duc "$@"\n' "$real" > "/usr/local/bin/$tool"; \
      chmod +x "/usr/local/bin/$tool"; \
    done \
 && httpx -version 2>&1 | head -1 \
 && nuclei -version 2>&1 | head -1 \
 && subfinder -version 2>&1 | head -1

# ── Layer 4: Kali apt 没有的 release binary 工具 ─────────────────────────────
# katana v1.6.1（https://github.com/projectdiscovery/katana/releases）
RUN wget -q --tries=3 --waitretry=5 \
        "https://github.com/projectdiscovery/katana/releases/download/v1.6.1/katana_1.6.1_linux_${TARGETARCH}.zip" \
        -O /tmp/katana.zip \
 && unzip -oq /tmp/katana.zip katana -d /usr/local/bin/ \
 && chmod +x /usr/local/bin/katana \
 && rm /tmp/katana.zip \
 && katana -version

# gau v2.2.4（https://github.com/lc/gau/releases）
RUN wget -q --tries=3 --waitretry=5 \
        "https://github.com/lc/gau/releases/download/v2.2.4/gau_2.2.4_linux_${TARGETARCH}.tar.gz" \
        -O /tmp/gau.tar.gz \
 && tar -xzf /tmp/gau.tar.gz -C /usr/local/bin gau \
 && chmod +x /usr/local/bin/gau \
 && rm /tmp/gau.tar.gz \
 && gau --version

# interactsh-client v1.3.1（https://github.com/projectdiscovery/interactsh/releases）
RUN wget -q --tries=3 --waitretry=5 \
        "https://github.com/projectdiscovery/interactsh/releases/download/v1.3.1/interactsh-client_1.3.1_linux_${TARGETARCH}.zip" \
        -O /tmp/interactsh.zip \
 && unzip -oq /tmp/interactsh.zip interactsh-client -d /usr/local/bin/ \
 && chmod +x /usr/local/bin/interactsh-client \
 && rm /tmp/interactsh.zip \
 && interactsh-client -version

# ── Layer 5: pip / npm 工具（Kali apt 没有）──────────────────────────────────
# semgrep: 官方推荐 pip（https://github.com/semgrep/semgrep，无 arm64 binary）
# spectral: 官方推荐 npm（https://github.com/stoplightio/spectral，6.x 无 arm64 binary）
RUN pip install --quiet --ignore-installed semgrep \
 && semgrep --version \
 && npm install -g @stoplight/spectral-cli \
 && spectral --version

# ── Layer 6: 专属安装层（逻辑复杂，Kali apt 没有）────────────────────────────
# jwt_tool（https://github.com/ticarpi/jwt_tool）requirements: termcolor cprint pycryptodomex requests ratelimit
RUN git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && pip install --quiet --ignore-installed -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool \
 && jwt_tool --help 2>&1 | head -1 || true

# dalfox v3.1.2（https://github.com/hahwul/dalfox/releases）
# 架构映射 arm64→aarch64 / amd64→x86_64；用 musl 静态版跨发行版可用；内部路径含子目录
RUN case "${TARGETARCH}" in \
      arm64) DALFOX_ARCH=aarch64 ;; \
      amd64) DALFOX_ARCH=x86_64  ;; \
      *)     DALFOX_ARCH="${TARGETARCH}" ;; \
    esac \
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/hahwul/dalfox/releases/download/v3.1.2/dalfox-v3.1.2-linux-${DALFOX_ARCH}-musl.tar.gz" \
        -O /tmp/dalfox.tar.gz \
 && tar -xzf /tmp/dalfox.tar.gz -C /tmp \
 && mv "/tmp/dalfox-v3.1.2-linux-${DALFOX_ARCH}-musl/dalfox" /usr/local/bin/dalfox \
 && chmod +x /usr/local/bin/dalfox \
 && rm -rf /tmp/dalfox.tar.gz "/tmp/dalfox-v3.1.2-linux-${DALFOX_ARCH}-musl" \
 && dalfox version 2>&1 | head -1

# ysoserial v0.0.6（https://github.com/frohoff/ysoserial，fat jar，经典 gadget 链兜底）
# 包装脚本硬编码 JDK8 运行（反序列化 gadget 依赖 JDK8 环境）
RUN wget -q --tries=3 --waitretry=5 \
        "https://github.com/frohoff/ysoserial/releases/download/v0.0.6/ysoserial-all.jar" \
        -O /opt/ysoserial.jar \
 && [ -s /opt/ysoserial.jar ] || { echo "[FATAL] ysoserial jar 下载失败"; exit 1; } \
 && printf '#!/bin/sh\nexec /opt/jdk8/bin/java -jar /opt/ysoserial.jar "$@"\n' > /usr/local/bin/ysoserial \
 && chmod +x /usr/local/bin/ysoserial \
 && ysoserial 2>&1 | head -1 || true

# ysomap（https://github.com/wh1t3p1g/ysomap，Java 反序列化利用框架，主力）
# clone master 自行 mvn 编译——release(2023-12) 落后 master(2025-02) 一年多利用链，
# 官方 README 明确推荐 clone 编译拿最新链。用 JDK8 编译并运行（XStream payload 依赖 JDK8）。
RUN git clone --depth 1 https://github.com/wh1t3p1g/ysomap.git /opt/ysomap-src \
 && cd /opt/ysomap-src \
 && JAVA_HOME=/opt/jdk8 PATH=/opt/jdk8/bin:$PATH mvn clean package -DskipTests -q \
 && JAR=$(find /opt/ysomap-src -name 'ysomap*.jar' -not -name '*sources*' -not -name '*original*' | head -1) \
 && [ -n "$JAR" ] || { echo "[FATAL] ysomap 编译产物未找到"; exit 1; } \
 && cp "$JAR" /opt/ysomap.jar \
 && rm -rf /opt/ysomap-src ~/.m2 \
 && printf '#!/bin/sh\nexec /opt/jdk8/bin/java -jar /opt/ysomap.jar "$@"\n' > /usr/local/bin/ysomap \
 && chmod +x /usr/local/bin/ysomap \
 && ysomap 2>&1 | head -1 || true

# ── Layer 7: browser-use 薄客户端提前 COPY（manifest 安装器需 command -v browser-use 命中）──
COPY deployments/tool-images/pentools/browser-use /usr/local/bin/browser-use
COPY deployments/tool-images/pentools/browser-svc.py /usr/local/bin/browser-svc.py
RUN chmod +x /usr/local/bin/browser-use /usr/local/bin/browser-svc.py

# ── Layer 8: manifest 驱动安装器（验证所有工具真实可用）──────────────────────
COPY deployments/tool-images/pentools/tools.yaml /tmp/tools.yaml
COPY scripts/build/install-from-manifest.sh /usr/local/bin/install-from-manifest.sh
RUN apt-get update \
 && chmod +x /usr/local/bin/install-from-manifest.sh \
 && TARGETARCH="${TARGETARCH}" /usr/local/bin/install-from-manifest.sh /tmp/tools.yaml \
 && rm -rf /var/lib/apt/lists/*

# ── Layer 9: nuclei 模板预拉 ──────────────────────────────────────────────────
# git clone 等价官方推荐的 nuclei -ut，CI 里比 nuclei 内部 fetcher 更可靠
RUN git clone --depth 1 https://github.com/projectdiscovery/nuclei-templates.git \
        /root/nuclei-templates 2>/dev/null \
 && echo "nuclei-templates: $(ls /root/nuclei-templates | wc -l) dirs" \
 || echo "[warn] nuclei-templates clone 失败，运行时 nuclei -ut 再拉"

# ── Layer 10: browser-use 专属 Python 3.12 venv ───────────────────────────────
# 隔离 3.12 venv：规避 Kali rolling Python 升到 3.14 后 asyncio.get_event_loop()
# 被移除导致 browser-use 静默失败（issue #4447）
RUN pip install --quiet --ignore-installed uv \
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

# ── Layer 11: agent 现写脚本常用 Python 库 ────────────────────────────────────
# 基础 HTTP/解析/协议库 + CTF 刚需（pwntools=pwn 交互、pycryptodome=crypto）
RUN pip install --quiet --ignore-installed \
        requests httpx aiohttp beautifulsoup4 lxml urllib3 \
        websockets websocket-client pysocks dnspython cryptography \
        pyjwt pyyaml xmltodict paramiko pymysql \
        pwntools pycryptodome \
 && python3 -c 'import requests, httpx, aiohttp, bs4, lxml, urllib3, websockets, websocket, socks, dns, cryptography, jwt, yaml, xmltodict, paramiko, pymysql, pwn, Crypto; print("python libs OK")'

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
