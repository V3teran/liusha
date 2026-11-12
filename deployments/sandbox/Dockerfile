#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够）
# liusha 沙盒镜像：Kali + 工具清单（internal/tools/manifest/tools.yaml）中的工具 + liusha（以 sandbox
# 子命令运行）。由部署方本地构建（compose 的 sandbox 服务 build 即指向本文件）；单镜像
# （deployments/single）以它为底。
#
# 层次：liusha 编译 → Kali 工具层（耗时长，按安装方式分层，每层末尾强制验证）→ 拷入 liusha。
# liusha 放在最后：改代码只重建最后一层，工具层全部命中缓存。
#
# 以 root 运行：渗透工具大量依赖原始套接字、低位端口与系统级安装路径；隔离边界是容器本身。

# ── liusha 编译：构建机原生架构交叉编译，零 QEMU ──
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS liusha
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/liusha ./cmd/liusha

# ── Kali + 工具 ──
FROM kalilinux/kali-rolling:latest

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    PIPX_HOME=/opt/pipx \
    PIPX_BIN_DIR=/usr/local/bin

# apt 重试配置（网络抖动不让整层 abort）
RUN printf 'Acquire::Retries "5";\nAcquire::http::Timeout "120";\nAcquire::https::Timeout "120";\n' \
      > /etc/apt/apt.conf.d/80-retries && \
    # 钉官方 CDN（kali.download）：http.kali.org 是重定向器，按地理位置导流到
    # 第三方镜像——个别镜像 TLS 证书损坏/失修时构建随机挂（实测 arm64 构建被
    # 导到证书校验失败的镜像源）。CDN 双架构全量分发，CI 与本地行为一致。
    # 镜像自带的源是 deb822 格式的 kali.sources，必须改写它本身——另写
    # sources.list 只会多出一个源，重定向器照旧生效。用 http：裸镜像没有 CA 证书，
    # https 源无法使用；包的完整性由 apt 的 GPG 签名校验保证，不依赖传输层。
    printf 'Types: deb\nURIs: http://kali.download/kali\nSuites: kali-rolling\nComponents: main contrib non-free non-free-firmware\nSigned-By: /usr/share/keyrings/kali-archive-keyring.gpg\n' \
      > /etc/apt/sources.list.d/kali.sources

# ══════════════════════════════════════════════════════════════════════════════
# Layer 1: 基础环境 + 构建依赖
# ══════════════════════════════════════════════════════════════════════════════
ARG TARGETARCH
ENV TARGETARCH=${TARGETARCH}

RUN set -eux \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl wget git unzip xz-utils tar file \
      python3 python3-pip python3-venv python3-dev pipx \
      nodejs npm \
      php-cli \
      golang maven \
      ruby-full ruby-dev libreadline-dev \
      default-mysql-client postgresql-client \
      build-essential libssl-dev libffi-dev \
      libgmp-dev libmpfr-dev libmpc-dev \
      cmake pkg-config \
 && rm -rf /var/lib/apt/lists/* \
 && printf 'tries=5\nwaitretry=10\ntimeout=60\nretry-connrefused=on\n' >> /etc/wgetrc \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${TARGETARCH}" \
      -O /usr/local/bin/yq \
 && chmod +x /usr/local/bin/yq

# ══════════════════════════════════════════════════════════════════════════════
# Layer 2: 双 JDK（Temurin 8 + 17）
# ══════════════════════════════════════════════════════════════════════════════
# Java 反序列化工具（ysomap/ysoserial）需要 JDK8，现代目标需要 JDK17
# 默认 java 指向 JDK8（兼容性最广），显式 java17 命令切换
RUN set -eux \
 && case "${TARGETARCH}" in \
      amd64) J_ARCH=x64 ;; \
      arm64) J_ARCH=aarch64 ;; \
      *) echo "不支持的架构: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/adoptium/temurin8-binaries/releases/download/jdk8u504-b01/OpenJDK8U-jdk_${J_ARCH}_linux_hotspot_8u504b01.tar.gz" \
      -O /tmp/jdk8.tar.gz \
 && mkdir -p /opt/jdk8 \
 && tar -xzf /tmp/jdk8.tar.gz -C /opt/jdk8 --strip-components=1 \
 && rm /tmp/jdk8.tar.gz \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.20.1+1/OpenJDK17U-jdk_${J_ARCH}_linux_hotspot_17.0.20.1_1.tar.gz" \
      -O /tmp/jdk17.tar.gz \
 && mkdir -p /opt/jdk17 \
 && tar -xzf /tmp/jdk17.tar.gz -C /opt/jdk17 --strip-components=1 \
 && rm /tmp/jdk17.tar.gz \
 && for b in java javac keytool jar; do \
      update-alternatives --install "/usr/bin/$b" "$b" "/opt/jdk8/bin/$b" 800; \
      update-alternatives --install "/usr/bin/$b" "$b" "/opt/jdk17/bin/$b" 170; \
      update-alternatives --set "$b" "/opt/jdk8/bin/$b"; \
    done \
 && printf '#!/bin/sh\nexec /opt/jdk8/bin/java "$@"\n' > /usr/local/bin/java8 \
 && printf '#!/bin/sh\nexec /opt/jdk17/bin/java "$@"\n' > /usr/local/bin/java17 \
 && chmod +x /usr/local/bin/java8 /usr/local/bin/java17 \
 && printf 'export JAVA_HOME=/opt/jdk8\n' > /etc/profile.d/java_home.sh

# JAVA_HOME 显式声明：mvn 启动脚本靠它定位 JVM（缺失时不可执行，且各架构基础
# 镜像默认值不一致——amd64 侥幸可用、arm64 失败）。指向 JDK8 与 java 命令的
# 默认指向一致：工具链里依赖 JDK 内部类的构建（如反序列化 payload 生成器）
# 只在 JDK8 下成立，JDK17 的模块封装会拒绝。
ENV JAVA_HOME=/opt/jdk8

# ══════════════════════════════════════════════════════════════════════════════
# Layer 3: apt 工具（48 个）
# ══════════════════════════════════════════════════════════════════════════════
# Kali 官方源直供的工具，apt 一次装齐
RUN set -eux \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      nmap sqlmap hydra ffuf feroxbuster wafw00f arjun \
      subfinder httpx-toolkit nuclei \
      phpggc trufflehog \
      seclists \
      masscan netexec \
      nikto wpscan \
      commix \
      metasploit-framework \
      john hashcat hash-identifier fcrackzip \
      binwalk foremost exiftool testdisk \
      steghide stegseek \
      gdb radare2 strace ltrace \
      patchelf checksec \
      proxychains4 tshark responder \
      curl jq \
      ghidra \
      afl++ \
 && rm -rf /var/lib/apt/lists/*

# ══════════════════════════════════════════════════════════════════════════════
# Layer 4: httpx 命名兼容 + PD 系工具 -duc wrapper
# ══════════════════════════════════════════════════════════════════════════════
# httpx: Kali 命令是 httpx-toolkit，包装成 /usr/local/bin/httpx（agent 调 httpx）
# nuclei/subfinder: 添加 -duc 参数，拦截 phone-home（api.pdtm.sh 版本检查）
RUN set -eux \
 && printf '#!/bin/sh\nexec httpx-toolkit -duc "$@"\n' > /usr/local/bin/httpx \
 && chmod +x /usr/local/bin/httpx \
 && for tool in nuclei subfinder; do \
      real=$(command -v "$tool") || { echo "❌ $tool 不存在"; exit 1; }; \
      mv "$real" "${real}.real"; \
      printf '#!/bin/sh\nexec %s.real -duc "$@"\n' "$real" > "$real"; \
      chmod +x "$real"; \
    done

# ══════════════════════════════════════════════════════════════════════════════
# Layer 5: pip 工具（1 个）+ pipx 工具（1 个）
# ══════════════════════════════════════════════════════════════════════════════
# semgrep 改用 pipx 隔离（避免 urllib3 冲突）
RUN pipx install semgrep

# ROPgadget 保持全局安装（无冲突）
RUN pip install --no-cache-dir --break-system-packages ROPgadget

# ══════════════════════════════════════════════════════════════════════════════
# Layer 6: pipx 工具（7 个）
# ══════════════════════════════════════════════════════════════════════════════
# 隔离 Python CLI 工具，避免依赖冲突
RUN pipx install prowler
RUN pipx install pacu
RUN pipx install cloudsplaining
RUN pipx install checkov
RUN pipx install kube-hunter
RUN pipx install bloodhound
RUN pipx install certipy-ad

# ══════════════════════════════════════════════════════════════════════════════
# Layer 7: npm 工具（1 个）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && npm install -g @stoplight/spectral-cli

# ══════════════════════════════════════════════════════════════════════════════
# Layer 8: gem 工具（4 个）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && gem install one_gadget seccomp-tools evil-winrm zsteg

# ══════════════════════════════════════════════════════════════════════════════
# Layer 9: release 工具（5 个）- GitHub release 压缩包
# ══════════════════════════════════════════════════════════════════════════════
# katana, fscan, gau, interactsh-client, sliver
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/projectdiscovery/katana/releases/download/v1.7.0/katana_1.7.0_linux_${TARGETARCH}.zip" \
      -O /tmp/katana.zip \
 && unzip -oq /tmp/katana.zip katana -d /usr/local/bin/ \
 && chmod +x /usr/local/bin/katana \
 && rm /tmp/katana.zip \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/shadow1ng/fscan/releases/download/v2.2.1/fscan_2.2.1_linux_$( [ "$TARGETARCH" = arm64 ] && echo arm64 || echo x64 )" \
      -O /usr/local/bin/fscan \
 && chmod +x /usr/local/bin/fscan \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/lc/gau/releases/download/v2.2.4/gau_2.2.4_linux_${TARGETARCH}.tar.gz" \
      -O /tmp/gau.tar.gz \
 && tar -xzf /tmp/gau.tar.gz -C /usr/local/bin gau \
 && chmod +x /usr/local/bin/gau \
 && rm /tmp/gau.tar.gz \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/projectdiscovery/interactsh/releases/download/v1.3.1/interactsh-client_1.3.1_linux_${TARGETARCH}.zip" \
      -O /tmp/interactsh.zip \
 && unzip -oq /tmp/interactsh.zip interactsh-client -d /usr/local/bin/ \
 && chmod +x /usr/local/bin/interactsh-client \
 && rm /tmp/interactsh.zip \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/BishopFox/sliver/releases/download/v1.7.7/sliver-server_linux-${TARGETARCH}" \
      -O /usr/local/bin/sliver \
 && chmod +x /usr/local/bin/sliver

# ══════════════════════════════════════════════════════════════════════════════
# Layer 10: release-bin 工具（2 个）- GitHub release 单二进制
# ══════════════════════════════════════════════════════════════════════════════
# chisel, linpeas
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/jpillora/chisel/releases/download/v1.12.0/chisel_1.12.0_linux_${TARGETARCH}.gz" \
      -O /tmp/chisel.gz \
 && gunzip /tmp/chisel.gz \
 && mv /tmp/chisel /usr/local/bin/chisel \
 && chmod +x /usr/local/bin/chisel \
 && wget -q --tries=3 --waitretry=5 -L \
      "https://github.com/carlospolop/PEASS-ng/releases/latest/download/linpeas.sh" \
      -O /usr/local/bin/linpeas \
 && chmod +x /usr/local/bin/linpeas

# ══════════════════════════════════════════════════════════════════════════════
# Layer 11: git 工具（1 个）
# ══════════════════════════════════════════════════════════════════════════════
# paramspider: git clone 后直接可用
RUN set -eux \
 && git clone --depth 1 https://github.com/devanshbatham/ParamSpider.git /opt/paramspider \
 && printf '#!/bin/sh\ncd /opt/paramspider && python3 paramspider.py "$@"\n' > /usr/local/bin/paramspider \
 && chmod +x /usr/local/bin/paramspider

# ══════════════════════════════════════════════════════════════════════════════
# Layer 12: ysomap（Java 反序列化利用框架）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/wh1t3p1g/ysomap.git /opt/ysomap \
 && cd /opt/ysomap \
 && mvn clean package -DskipTests \
 && printf '#!/bin/sh\nexec java -jar /opt/ysomap/target/ysomap.jar "$@"\n' > /usr/local/bin/ysomap \
 && chmod +x /usr/local/bin/ysomap

# ══════════════════════════════════════════════════════════════════════════════
# Layer 13: ysoserial（Java 反序列化经典 gadget）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/frohoff/ysoserial/releases/download/v0.0.6/ysoserial-all.jar" \
      -O /opt/ysoserial.jar \
 && printf '#!/bin/sh\nexec java -jar /opt/ysoserial.jar "$@"\n' > /usr/local/bin/ysoserial \
 && chmod +x /usr/local/bin/ysoserial

# ══════════════════════════════════════════════════════════════════════════════
# Layer 14: jwt_tool（JWT 攻击）- venv 隔离
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && python3 -m venv /opt/venvs/jwt_tool \
 && /opt/venvs/jwt_tool/bin/pip install --no-cache-dir -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec /opt/venvs/jwt_tool/bin/python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool

# ══════════════════════════════════════════════════════════════════════════════
# Layer 15: dalfox（XSS 扫描器）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && DALFOX_ARCH=$( [ "$TARGETARCH" = arm64 ] && echo aarch64 || echo x86_64 ) \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/hahwul/dalfox/releases/download/v3.2.2/dalfox-v3.2.2-linux-${DALFOX_ARCH}-musl.tar.gz" \
      -O /tmp/dalfox.tar.gz \
 && tar -xzf /tmp/dalfox.tar.gz -C /tmp \
 && mv /tmp/dalfox-v3.2.2-linux-${DALFOX_ARCH}-musl/dalfox /usr/local/bin/dalfox \
 && chmod +x /usr/local/bin/dalfox \
 && rm -rf /tmp/dalfox*

# ══════════════════════════════════════════════════════════════════════════════
# Layer 16: SSTImap（SSTI 模板注入）- venv 隔离
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/vladko312/SSTImap.git /opt/SSTImap \
 && python3 -m venv /opt/venvs/SSTImap \
 && /opt/venvs/SSTImap/bin/pip install --no-cache-dir -r /opt/SSTImap/requirements.txt \
 && printf '#!/bin/sh\nexec /opt/venvs/SSTImap/bin/python3 /opt/SSTImap/sstimap.py "$@"\n' > /usr/local/bin/SSTImap \
 && chmod +x /usr/local/bin/SSTImap

# ══════════════════════════════════════════════════════════════════════════════
# Layer 16: 安装 uv（现代 Python 包管理器，SSRFmap 依赖）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && curl -LsSf https://astral.sh/uv/install.sh | sh \
 && ln -s /root/.local/bin/uv /usr/local/bin/uv

# ══════════════════════════════════════════════════════════════════════════════
# Layer 17: SSRFmap（SSRF 利用）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/swisskyrepo/SSRFmap.git /opt/SSRFmap \
 && cd /opt/SSRFmap \
 && uv sync --frozen \
 && printf '#!/bin/sh\ncd /opt/SSRFmap && exec uv run --no-sync python3 ssrfmap.py "$@"\n' > /usr/local/bin/SSRFmap \
 && chmod +x /usr/local/bin/SSRFmap

# ══════════════════════════════════════════════════════════════════════════════
# Layer 18: RsaCtfTool（RSA 密码学攻击）- venv 隔离
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/RsaCtfTool/RsaCtfTool.git /opt/RsaCtfTool \
 && python3 -m venv /opt/venvs/RsaCtfTool \
 && /opt/venvs/RsaCtfTool/bin/pip install --no-cache-dir -e /opt/RsaCtfTool \
 && printf '#!/bin/sh\nexec /opt/venvs/RsaCtfTool/bin/python3 /opt/RsaCtfTool/RsaCtfTool.py "$@"\n' > /usr/local/bin/RsaCtfTool \
 && chmod +x /usr/local/bin/RsaCtfTool

# ══════════════════════════════════════════════════════════════════════════════
# Layer 19: angr（二进制符号执行）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && python3 -m venv /opt/angr-venv \
 && /opt/angr-venv/bin/pip install --no-cache-dir angr \
 && printf '#!/bin/sh\nexec /opt/angr-venv/bin/python "$@"\n' > /usr/local/bin/angr-python \
 && chmod +x /usr/local/bin/angr-python \
 && ln -s /usr/local/bin/angr-python /usr/local/bin/angr

# ══════════════════════════════════════════════════════════════════════════════
# Layer 20: browser-use（浏览器自动化）
#
# 版本钉死：浏览器服务（internal/sandbox/browser/service.py）用 0.x 的 BrowserSession /
# actor.Page 接口，1.0 移除了它们——升级须先移植服务脚本，并同步沙盒的能力探测。
# 命令里的 browser-use 由 liusha sandbox 启动时生成（以本 venv 运行内置客户端），这里不另装入口。
# chromium 预装：browser_use 启动浏览器经 `uvx playwright install chromium` 拉取
# （~114MB + 系统依赖）。预装进镜像——离线/弱网沙箱下运行时下载会直接挂掉浏览器栈。
# PLAYWRIGHT_BROWSERS_PATH 钉在固定目录：缺省位置随 $HOME 走，而沙盒执行命令时 HOME 指向
# 任务目录，浏览器会找不到预装的 chromium、在每个任务目录里各下载一份（近 1GB）。
# uvx 真实语义 = `uv tool run`（uv 按 argv[0] 分发）。
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN set -eux \
 && python3 -m venv /opt/browser-use-venv \
 && /opt/browser-use-venv/bin/pip install --no-cache-dir 'browser-use==0.13.14' \
 && printf '#!/bin/sh\nexec /usr/local/bin/uv tool run "$@"\n' > /usr/local/bin/uvx \
 && chmod +x /usr/local/bin/uvx \
 && uvx playwright install chromium --with-deps

# ══════════════════════════════════════════════════════════════════════════════
# Layer 21: stegoveritas（隐写分析）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && pip install --no-cache-dir --break-system-packages --ignore-installed stegoveritas \
 && stegoveritas_install_deps

# ══════════════════════════════════════════════════════════════════════════════
# Layer 22: vol（Volatility 3 内存取证）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && pip install --no-cache-dir --break-system-packages volatility3

# ══════════════════════════════════════════════════════════════════════════════
# Layer 23: peirates（K8s 渗透）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/inguardians/peirates/releases/download/v1.1.31/peirates-linux-${TARGETARCH}.tar.xz" \
      -O /tmp/peirates.tar.xz \
 && tar -xJf /tmp/peirates.tar.xz --strip-components=1 -C /usr/local/bin "peirates-linux-${TARGETARCH}/peirates" \
 && chmod +x /usr/local/bin/peirates \
 && rm /tmp/peirates.tar.xz

# ══════════════════════════════════════════════════════════════════════════════
# Layer 24: kube-bench（K8s CIS 基准测试）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/aquasecurity/kube-bench/releases/download/v0.16.0/kube-bench_0.16.0_linux_${TARGETARCH}.tar.gz" \
      -O /tmp/kube-bench.tar.gz \
 && tar -xzf /tmp/kube-bench.tar.gz -C /usr/local/bin kube-bench \
 && chmod +x /usr/local/bin/kube-bench \
 && rm /tmp/kube-bench.tar.gz

# ══════════════════════════════════════════════════════════════════════════════
# Layer 25: kubectl
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && KUBECTL_ARCH=$([ "$TARGETARCH" = "amd64" ] && echo "amd64" || echo "arm64") \
 && KUBECTL_VERSION=$(wget -qO- https://dl.k8s.io/release/stable.txt) \
 && wget -q --tries=3 --waitretry=5 \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${KUBECTL_ARCH}/kubectl" \
      -O /usr/local/bin/kubectl \
 && chmod +x /usr/local/bin/kubectl

# ══════════════════════════════════════════════════════════════════════════════
# Layer 26: ligolo-ng（隧道代理）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/nicocha30/ligolo-ng/releases/download/v0.9.1/ligolo-ng_agent_0.9.1_linux_${TARGETARCH}.tar.gz" \
      -O /tmp/ligolo.tar.gz \
 && tar -xzf /tmp/ligolo.tar.gz -C /usr/local/bin agent \
 && mv /usr/local/bin/agent /usr/local/bin/ligolo-ng \
 && chmod +x /usr/local/bin/ligolo-ng \
 && rm /tmp/ligolo.tar.gz

# ══════════════════════════════════════════════════════════════════════════════
# Layer 27: pwninit（pwn 题模板生成）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/io12/pwninit/releases/download/3.3.3/pwninit" \
      -O /usr/local/bin/pwninit \
 && chmod +x /usr/local/bin/pwninit

# ══════════════════════════════════════════════════════════════════════════════
# Layer 28: cloudfox（云安全枚举）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/BishopFox/cloudfox/releases/download/v2.0.5/cloudfox-linux-${TARGETARCH}.zip" \
      -O /tmp/cloudfox.zip \
 && unzip -oq /tmp/cloudfox.zip cloudfox/cloudfox -d /tmp/ \
 && mv /tmp/cloudfox/cloudfox /usr/local/bin/cloudfox \
 && chmod +x /usr/local/bin/cloudfox \
 && rm /tmp/cloudfox.zip

# ══════════════════════════════════════════════════════════════════════════════
# Layer 29: trivy（容器镜像扫描）
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && wget -q --tries=3 --waitretry=5 \
      "https://github.com/aquasecurity/trivy/releases/download/v0.74.0/trivy_0.74.0_Linux-64bit.tar.gz" \
      -O /tmp/trivy.tar.gz \
 && tar -xzf /tmp/trivy.tar.gz -C /usr/local/bin trivy \
 && chmod +x /usr/local/bin/trivy \
 && rm /tmp/trivy.tar.gz

# ══════════════════════════════════════════════════════════════════════════════
# Layer 30: gdb + pwndbg
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && git clone --depth 1 https://github.com/pwndbg/pwndbg.git /opt/pwndbg \
 && cd /opt/pwndbg \
 && ./setup.sh

# ══════════════════════════════════════════════════════════════════════════════
# Layer 31: nuclei 模板预拉
# ══════════════════════════════════════════════════════════════════════════════
RUN set -eux \
 && nuclei -ut

# ══════════════════════════════════════════════════════════════════════════════
# liusha 沙盒服务
# ══════════════════════════════════════════════════════════════════════════════
COPY --from=liusha /out/liusha /usr/local/bin/liusha

LABEL org.opencontainers.image.source="https://github.com/V3teran/liusha" \
      org.opencontainers.image.description="liusha 沙盒：Kali 工具集 + liusha sandbox"

# 浏览器服务用 browser-use 专属 venv（Layer 20）：与随发行版滚动的系统 Python 隔离。
ENV LIUSHA_SANDBOX_LISTEN_ADDR=0.0.0.0:8591 \
    LIUSHA_SANDBOX_BROWSER_PYTHON=/opt/browser-use-venv/bin/python
WORKDIR /root
EXPOSE 8591
ENTRYPOINT ["/usr/local/bin/liusha"]
CMD ["sandbox"]
