# syntax=docker/dockerfile:1.7
# liusha pentools 镜像：六域渗透沙箱 = 六能力域工具集 + browser-use + sandbox-server
#   六域：web 漏洞挖掘 / 二进制漏洞挖掘 / 漏洞利用 / 多阶段渗透 / 云攻击 / CTF
#
# 设计要点：
#   - base = kalilinux/kali-rolling，但【绝不装 kali-linux-headless 元包】
#     （headless 会拉 ~160 个 GUI/驱动工具，镜像白膨胀 3-4GB）
#     只 apt install 需要的工具——最大化利用 Kali 维护的安全工具仓（版本新、免下载）
#   - Kali apt 直供的走 apt（含 ghidra/radare2/afl++/commix/feroxbuster/metasploit/
#     netexec 走安装器 Layer 8；nmap/sqlmap 等 web 基线在 Layer 2）
#   - Kali apt 没有的走官方推荐：katana/gau/interactsh(release)、semgrep(pip)、
#     jwt_tool(git)、ysoserial(jar)、ysomap(git+mvn)、dalfox(release)、spectral(npm)，
#     以及六域新增的 release/venv/git 专属层（trivy/kubectl/angr/volatility3/RsaCtfTool 等）
#   - httpx 在 Kali 包名是 httpx-toolkit，包装成 /usr/local/bin/httpx 给 agent 用
#   - 双 JDK（Temurin）：JDK8(8u352)默认 + JDK17(17.0.19) 现代目标；java8/java17 显式切换。
#     ysomap/ysoserial 包装脚本硬编码 JDK8 运行。ghidra 走 apt（自带拉 JDK，不占这两个）。
#   - runtime：Go/PHP/Node/gcc(apt) + Ruby(ruby-full，供 gem 系工具) + Python
#   - 重型/死 pin 的 Python 工具各自独立 venv：angr / volatility3 / RsaCtfTool（不污染系统 py）
#   - PEP 668 在 Kali 开启，pip 需 --break-system-packages(env) + --ignore-installed
#   - browser-use 跑独立 Python 3.12 venv（隔离 Kali rolling python）
#   - multi-stage：Stage 0 编译 sandbox-server，Stage 1 运行镜像
#   - linux/amd64-only（CI runner = ubuntu-24.04）；架构变体全硬编码，无 arm64 分支
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
    PIPX_HOME=/opt/pipx \
    PIPX_BIN_DIR=/usr/local/bin \
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
# amd64-only：Temurin 架构名硬定 x64。版本段无连字符：8u352b08 / 17.0.19_10。
RUN set -eux \
 && J_ARCH=x64 \
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
        nmap sqlmap hydra ffuf feroxbuster wafw00f arjun \
        subfinder httpx-toolkit nuclei \
        phpggc trufflehog \
        seclists \
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

# ── Layer 5b: ruby + pipx 基座（供 Layer 8 安装器的 gem/pipx 系工具）───────────
# 必须在 Layer 8 之前就位——安装器逐行处理 tools.yaml，轮到 gem(zsteg/one_gadget/
# seccomp-tools/evil-winrm)/pipx(prowler/pacu/cloudsplaining/netexec/bloodhound/certipy)
# 工具时 ruby/pipx 需已存在。pipx 命令目录由 ENV PIPX_BIN_DIR=/usr/local/bin 定向到 PATH。
RUN apt-get update \
 && apt-get install -y --no-install-recommends ruby-full pipx \
 && rm -rf /var/lib/apt/lists/* \
 && ruby --version \
 && pipx --version

# ── Layer 6: 专属安装层（逻辑复杂，Kali apt 没有）────────────────────────────
# jwt_tool（https://github.com/ticarpi/jwt_tool）requirements: termcolor cprint pycryptodomex requests ratelimit
RUN git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && pip install --quiet --ignore-installed -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool \
 && jwt_tool --help 2>&1 | head -1 || true

# dalfox v3.1.2（https://github.com/hahwul/dalfox/releases）
# amd64-only：架构名硬定 x86_64；用 musl 静态版跨发行版可用；内部路径含子目录
RUN DALFOX_ARCH=x86_64 \
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

# ── Layer 6b: injection 专项（git clone + python wrapper，命令名 = tools.yaml name）──
# SSTImap（https://github.com/vladko312/SSTImap）模板注入自动利用
# SSRFmap（https://github.com/swisskyrepo/SSRFmap）SSRF 自动利用
RUN git clone --depth 1 https://github.com/vladko312/SSTImap.git /opt/SSTImap \
 && pip install --quiet --ignore-installed -r /opt/SSTImap/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/SSTImap/sstimap.py "$@"\n' > /usr/local/bin/SSTImap \
 && chmod +x /usr/local/bin/SSTImap \
 && SSTImap --help >/dev/null 2>&1 || true \
 && git clone --depth 1 https://github.com/swisskyrepo/SSRFmap.git /opt/SSRFmap \
 && pip install --quiet --ignore-installed -r /opt/SSRFmap/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/SSRFmap/ssrfmap.py "$@"\n' > /usr/local/bin/SSRFmap \
 && chmod +x /usr/local/bin/SSRFmap \
 && SSRFmap --help >/dev/null 2>&1 || true

# ── Layer 6c: gdb + pwndbg（pwn 调试主场；命令名 gdb）────────────────────────
# apt gdb + git pwndbg + setup.sh；pwndbg 装 gdbinit 让 gdb 启动即加载插件。
RUN apt-get update \
 && apt-get install -y --no-install-recommends gdb \
 && rm -rf /var/lib/apt/lists/* \
 && git clone --depth 1 https://github.com/pwndbg/pwndbg.git /opt/pwndbg \
 && cd /opt/pwndbg \
 && ./setup.sh --update 2>/dev/null || ./setup.sh 2>/dev/null || pip install --quiet --ignore-installed -e /opt/pwndbg \
 && grep -q pwndbg /root/.gdbinit 2>/dev/null || echo "source /opt/pwndbg/gdbinit.py" >> /root/.gdbinit \
 && gdb --version | head -1

# ── Layer 6d: cloud/container release 二进制（资产名/布局各异，逐个硬编码，amd64）──
# 资产名全部经 gh release 实测（非 latest_download 猜测）：
#   trivy      v0.73.0  trivy_0.73.0_Linux-64bit.tar.gz          → 根部 trivy
#   kubectl    v1.36.3  dl.k8s.io stable 官方 URL                → 裸二进制
#   peirates   v1.29a   peirates-linux-amd64.tar.xz              → 子目录 peirates-linux-amd64/peirates
#   kube-bench v0.16.0  kube-bench_0.16.0_linux_amd64.tar.gz     → 根部 kube-bench + cfg/（必须一起解到 /opt）
#   ligolo-ng  v0.9.1   ligolo-ng_proxy_0.9.1_linux_amd64.tar.gz → 解包 proxy，改名 ligolo-ng
#   pwninit    3.3.3    裸预编译二进制 pwninit（无 tag v 前缀，不装 rust）
#   cloudfox   v2.0.5   cloudfox-linux-amd64.zip                 → 子目录 cloudfox/cloudfox
RUN set -eux \
 # trivy（漏洞库运行时按需拉，构建期不烘焙）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/aquasecurity/trivy/releases/download/v0.73.0/trivy_0.73.0_Linux-64bit.tar.gz" \
        -O /tmp/trivy.tgz \
 && tar -xzf /tmp/trivy.tgz -C /usr/local/bin trivy \
 && chmod +x /usr/local/bin/trivy && rm /tmp/trivy.tgz && trivy --version | head -1 \
 # kubectl（官方 dl.k8s.io stable）
 && wget -q --tries=3 --waitretry=5 \
        "https://dl.k8s.io/release/v1.36.3/bin/linux/amd64/kubectl" \
        -O /usr/local/bin/kubectl \
 && chmod +x /usr/local/bin/kubectl && kubectl version --client 2>&1 | head -1 \
 # peirates（.tar.xz，子目录）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/inguardians/peirates/releases/download/v1.29a/peirates-linux-amd64.tar.xz" \
        -O /tmp/peirates.txz \
 && tar -xJf /tmp/peirates.txz -C /tmp \
 && mv /tmp/peirates-linux-amd64/peirates /usr/local/bin/peirates \
 && chmod +x /usr/local/bin/peirates && rm -rf /tmp/peirates.txz /tmp/peirates-linux-amd64 \
 && command -v peirates \
 # kube-bench（二进制 + cfg/ 基线目录一起放 /opt/kube-bench，wrapper 指定 --config-dir）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/aquasecurity/kube-bench/releases/download/v0.16.0/kube-bench_0.16.0_linux_amd64.tar.gz" \
        -O /tmp/kb.tgz \
 && mkdir -p /opt/kube-bench && tar -xzf /tmp/kb.tgz -C /opt/kube-bench && rm /tmp/kb.tgz \
 && printf '#!/bin/sh\nexec /opt/kube-bench/kube-bench --config-dir /opt/kube-bench/cfg "$@"\n' > /usr/local/bin/kube-bench \
 && chmod +x /usr/local/bin/kube-bench && test -d /opt/kube-bench/cfg \
 # ligolo-ng proxy 端（解包 proxy，改名）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/nicocha30/ligolo-ng/releases/download/v0.9.1/ligolo-ng_proxy_0.9.1_linux_amd64.tar.gz" \
        -O /tmp/ligolo.tgz \
 && tar -xzf /tmp/ligolo.tgz -C /tmp proxy \
 && mv /tmp/proxy /usr/local/bin/ligolo-ng \
 && chmod +x /usr/local/bin/ligolo-ng && rm /tmp/ligolo.tgz && command -v ligolo-ng \
 # pwninit（裸预编译二进制，无 tag v 前缀）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/io12/pwninit/releases/download/3.3.3/pwninit" \
        -O /usr/local/bin/pwninit \
 && chmod +x /usr/local/bin/pwninit && command -v pwninit \
 # cloudfox（zip 内子目录 cloudfox/cloudfox）
 && wget -q --tries=3 --waitretry=5 \
        "https://github.com/BishopFox/cloudfox/releases/download/v2.0.5/cloudfox-linux-amd64.zip" \
        -O /tmp/cloudfox.zip \
 && unzip -oq /tmp/cloudfox.zip -d /tmp/cloudfox-x \
 && mv /tmp/cloudfox-x/cloudfox/cloudfox /usr/local/bin/cloudfox \
 && chmod +x /usr/local/bin/cloudfox && rm -rf /tmp/cloudfox.zip /tmp/cloudfox-x \
 && cloudfox --version 2>&1 | head -1 || true

# ── Layer 6e: 重依赖/死 pin 的 Python 工具（各自独立 venv，不污染系统 py3.13）──
# volatility3：pip 装，console_script=vol（pyproject [project.scripts] 实测）。符号表运行时按需拉。
# angr：符号执行，依赖极重且 pin 死；无官方 CLI → wrapper 执行 venv python（agent 写 angr 脚本跑）。
# RsaCtfTool：PyPI 同名是抢注包，必须 git clone；死 pin(cryptography==39/gmpy2==2.2.1/urllib3==1.26.5)
#   在 py3.13 装轮子有风险 → venv 内装 requirements，console_script=RsaCtfTool。
RUN set -eux \
 # volatility3 venv
 && python3 -m venv /opt/venv-vol \
 && /opt/venv-vol/bin/pip install --quiet volatility3 \
 && ln -sf /opt/venv-vol/bin/vol /usr/local/bin/vol \
 && vol --help >/dev/null 2>&1 && echo "vol OK" \
 # angr venv（无 CLI，wrapper 跑 venv python -c 兜底 sanity）
 && python3 -m venv /opt/venv-angr \
 && /opt/venv-angr/bin/pip install --quiet angr \
 && /opt/venv-angr/bin/python -c 'import angr' \
 && printf '#!/bin/sh\nexec /opt/venv-angr/bin/python "$@"\n' > /usr/local/bin/angr \
 && chmod +x /usr/local/bin/angr && command -v angr \
 # RsaCtfTool venv（git clone + 死 pin requirements）
 && python3 -m venv /opt/venv-rsactf \
 && git clone --depth 1 https://github.com/RsaCtfTool/RsaCtfTool.git /opt/RsaCtfTool-src \
 && /opt/venv-rsactf/bin/pip install --quiet -r /opt/RsaCtfTool-src/requirements.txt \
 && /opt/venv-rsactf/bin/pip install --quiet /opt/RsaCtfTool-src \
 && ln -sf /opt/venv-rsactf/bin/RsaCtfTool /usr/local/bin/RsaCtfTool \
 && RsaCtfTool --help >/dev/null 2>&1 && echo "RsaCtfTool OK"

# ── Layer 6f: stegoveritas（pip 装本体 + stegoveritas_install_deps 装系统二进制依赖）──
# 只装本体命令在（command -v）但缺 zsteg/foremost/exiftool 等系统依赖时运行时炸，
# 官方要求装完跑 stegoveritas_install_deps 补齐（本镜像已另装 zsteg/foremost/exiftool，此处兜底其余）。
RUN pip install --quiet --ignore-installed stegoveritas \
 && (stegoveritas_install_deps 2>/dev/null || true) \
 && command -v stegoveritas


# ── Layer 7: browser-use 薄客户端提前 COPY（manifest 安装器需 command -v browser-use 命中）──
COPY deployments/tool-images/pentools/browser-use /usr/local/bin/browser-use
COPY deployments/tool-images/pentools/browser-svc.py /usr/local/bin/browser-svc.py
RUN chmod +x /usr/local/bin/browser-use /usr/local/bin/browser-svc.py

# ── Layer 8: manifest 驱动安装器（验证所有工具真实可用）──────────────────────
# tools.yaml 与安装脚本都是**纯构建期输入**——运行时 runner 读自己文件系统那份 tools.yaml，
# 镜像内不需要留。用 bind-mount 挂进来用完即走，不落任何层（COPY 会残留：层叠加，
# 后层 rm 只加 whiteout、基础层文件仍在、镜像不缩）。
RUN --mount=type=bind,source=deployments/tool-images/pentools/tools.yaml,target=/tmp/tools.yaml \
    --mount=type=bind,source=scripts/build/install-from-manifest.sh,target=/tmp/install-from-manifest.sh \
    apt-get update \
 && TARGETARCH="${TARGETARCH}" bash /tmp/install-from-manifest.sh /tmp/tools.yaml \
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
