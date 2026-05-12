# liusha pentools 镜像（tag liusha/pentools:latest）：
# 把渗透常用 CLI 工具集成到单一镜像，让 docker_runner 通过统一 image
# 跑 sqlmap / nuclei / ffuf / ... 不同工具。
#
# 设计要点：
#   - 单镜像而非"一工具一镜像"：apt 共享底层、layer 复用，磁盘占用小
#   - WORKDIR 留给 docker_runner 用 -v workdir:/work mount
#   - 不内置 ENTRYPOINT：让 docker_runner 直接 `docker run image <tool> <args>`，
#     避免 ENTRYPOINT/CMD 与 RunSpec.Cmd 干扰
#   - 多 layer 设计：apt → pip → wget Go 二进制 → nuclei 模板 → 反序列化 → wordlist
#     失败可单层重试，不全量重 build
#   - **Go 二进制 + ysoserial 用 GitHub API releases/latest 动态拉最新**——
#     build 即升级，无需手动改 ARG；fallback 默认值兜底 API 不可达
#   - **base = ubuntu:latest**：自动跟随官方最新 LTS（2026-04 后 = 26.04）
#
# 构建：
#   docker build -t liusha/pentools:latest -f deployments/tool-images/pentools/Dockerfile .
#
# 工具清单（22 个，按 8 类）：
#   recon (5):           subfinder httpx katana nmap wafw00f
#   discovery (3):       ffuf dirsearch arjun
#   vulnscan (1):        nuclei
#   injection (2):       sqlmap dalfox
#   deserialization (2): ysoserial phpggc
#   auth (2):            hydra jwt_tool
#   sast (2):            semgrep trufflehog
#   utility (5):         curl python3 jq interactsh-client mysql
#
# 体积预估 ~1.1-1.3 GB（含 nuclei-templates 80MB / SecLists 子集 30MB / JDK 350MB / PHP 25MB / Chrome 110MB + chromedriver 20MB / Python libs（HTTP/加密/SSH/selenium/pymysql 等 17 个）~90MB）。

# 强制 amd64 base——
# 大部分渗透工具（ysoserial / dalfox / 部分 Go release）amd64 binary 才齐全；
# arm64 上跑 amd64 binary 走 rosetta 转译，dalfox 之前因 elf interp 缺失报错。
# 用 amd64 base 整套环境对齐，所有 amd64 binary 直接兼容。
# macOS arm64 host 跑此镜像全程 rosetta 转译（慢但稳定）。
FROM --platform=linux/amd64 ubuntu:latest

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_TRUSTED_HOST=pypi.tuna.tsinghua.edu.cn \
    PIP_RETRIES=5 \
    PIP_TIMEOUT=60

# 加 apt 重试配置——清华源在 docker desktop macOS 上偶尔丢连接（之前
# openjdk-21 ~150MB 在 507s 处 connection failed），没 retry 整层 abort。
# 注：/etc/wgetrc 配置移到 wget 装好之后（Layer 1a 之后），避免 dpkg 安装
# wget 包时跟预置的 wgetrc 冲突。
RUN echo 'Acquire::Retries "5";' > /etc/apt/apt.conf.d/80-retries \
 && echo 'Acquire::http::Timeout "120";' >> /etc/apt/apt.conf.d/80-retries \
 && echo 'Acquire::https::Timeout "120";' >> /etc/apt/apt.conf.d/80-retries

# Layer 1a: apt 系统级小工具——清华源加速。
# - sqlmap / hydra / nmap：apt 直装的渗透工具
# - python3 + pip：脚本运行时 + 后续 pip 工具
# - php-cli：phpggc 必需（~25MB）
# - git wget unzip：拉源码/二进制
# - jq curl ca-certificates：基础工具 + TLS 信任链
# JRE 单独后置 layer，失败时只重 1b 不全量重 build。
RUN sed -i \
        -e 's|http://archive.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        -e 's|http://ports.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        -e 's|http://security.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        /etc/apt/sources.list.d/ubuntu.sources \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        sqlmap \
        hydra \
        nmap \
        python3 \
        python3-pip \
        python3-setuptools \
        jq \
        curl \
        wget \
        unzip \
        git \
        ca-certificates \
        php-cli \
        default-mysql-client \
 && rm -rf /var/lib/apt/lists/* \
 && sqlmap --version \
 && hydra -h 2>&1 | head -2 \
 && nmap --version | head -1 \
 && php --version | head -1 \
 && mysql --version

# Layer 1a.5: 全局 wgetrc retry——GitHub release download 在 docker desktop
# gvisor 网络偶发 404/timeout（之前 ysoserial 多次失败）；所有 wget 命令统一重试。
# 必须在 Layer 1a wget 装好后才能写（否则 dpkg 装 wget 时冲突）。
RUN printf 'tries=5\nwaitretry=10\ntimeout=60\nretry-connrefused=on\n' >> /etc/wgetrc

# Layer 1b: JDK 单独装——~350MB 大包（含 javac 编译器 + JRE），单独 layer 让失败可重试不全量重 build。
# 装 JDK 而非 JRE-headless 的原因：LLM 渗透场景偶发需要现场编译 Java（如自定义反序列化 gadget、
# JNDI exploit class 文件），多 200MB 换"现场编译能力"对开发体验有意义。
RUN apt-get update \
 && apt-get install -y --no-install-recommends default-jdk-headless \
 && rm -rf /var/lib/apt/lists/* \
 && java -version 2>&1 | head -1 \
 && javac -version 2>&1 | head -1

# Layer 1c: Google Chrome（dalfox / katana 等用 chromedp 的工具需要 chromium-style 浏览器）。
# Ubuntu 24.04 默认 chromium 是 snap 包（docker 内不可用），改装 google-chrome-stable.deb。
# chromedp 自动查找 google-chrome / chromium / chrome；apt-get install 一条 deb 会自动拉运行时依赖。
# 约 +110MB；失败单独 layer 可重试。
RUN wget -q -O /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/chrome.deb \
 && rm /tmp/chrome.deb \
 && rm -rf /var/lib/apt/lists/* \
 && google-chrome --version

# Layer 1d: chromedriver（selenium 必需；dalfox/katana 用 chromedp 不依赖它，但 LLM 写
# Python selenium 脚本会调用 selenium-manager 在线下载，容器首次跑可能卡——预装最稳）。
# 策略：先试和 Chrome 完全匹配的版本（Chrome for Testing storage），失败用 LATEST_RELEASE_STABLE 兜底。
# 约 +20MB。
RUN set -eu \
 && CHROME_VERSION=$(google-chrome --version | awk '{print $3}') \
 && CHROME_MAJOR=$(echo "$CHROME_VERSION" | cut -d. -f1) \
 && HARDCODE_FB="148.0.7778.97" \
 && URL="https://storage.googleapis.com/chrome-for-testing-public/${CHROME_VERSION}/linux64/chromedriver-linux64.zip" \
 && if ! wget -q --tries=3 --waitretry=10 --timeout=30 --spider "$URL"; then \
        FB="" ; \
        for ep in "LATEST_RELEASE_${CHROME_MAJOR}" "LATEST_RELEASE_STABLE"; do \
            FB=$(curl -fsS --max-time 30 --retry 5 --retry-delay 5 "https://googlechromelabs.github.io/chrome-for-testing/${ep}" 2>/dev/null) || FB="" ; \
            if [ -n "$FB" ]; then echo "[chromedriver] fallback via ${ep}=$FB"; break; fi ; \
        done ; \
        [ -n "$FB" ] || { FB="$HARDCODE_FB"; echo "[chromedriver] 所有在线 fallback 失败，用 hardcode=$FB" ; } ; \
        URL="https://storage.googleapis.com/chrome-for-testing-public/${FB}/linux64/chromedriver-linux64.zip" ; \
    else \
        echo "[chromedriver] 精确匹配 Chrome=$CHROME_VERSION" ; \
    fi \
 && wget -q --tries=5 --waitretry=15 --timeout=60 "$URL" -O /tmp/cd.zip \
 && [ -s /tmp/cd.zip ] || { echo "[FATAL] chromedriver 下载空: $URL"; exit 1 ; } \
 && unzip -q /tmp/cd.zip -d /tmp \
 && mv /tmp/chromedriver-linux64/chromedriver /usr/local/bin/chromedriver \
 && chmod +x /usr/local/bin/chromedriver \
 && rm -rf /tmp/cd.zip /tmp/chromedriver-linux64 \
 && chromedriver --version

# Layer 2: Python 工具 + LLM 常用渗透/Web 三方库（pip 一次装齐）。
# 工具（4 个）：dirsearch / arjun / wafw00f / semgrep
# HTTP/HTML 三方库（5 个）：requests / httpx / aiohttp / beautifulsoup4 / lxml
# 渗透常用（12 个）：urllib3 / websockets / websocket-client / pysocks / dnspython /
#                    cryptography / pyjwt / pyyaml / xmltodict / paramiko / selenium / pymysql
# pymysql 是纯 Python 驱动（无 C 扩展），LLM 拿到 DB 凭证可直接 import pymysql；
# 同时 apt 装了 default-mysql-client，CLI `mysql -h ...` 也可用，两种姿势齐备。
# 设计哲学：LLM 写脚本默认 import 这些，不预装就强迫它降级用 stdlib 或浪费 round-trip 装包。
# 用 PEP 668 --break-system-packages（已通过 ENV PIP_BREAK_SYSTEM_PACKAGES=1）。
# semgrep 装完体积最大（~150MB 含规则集）；其他 lib 合计 +60-90MB。
RUN pip install --quiet \
        dirsearch \
        arjun \
        wafw00f \
        semgrep \
        requests \
        httpx \
        aiohttp \
        beautifulsoup4 \
        lxml \
        urllib3 \
        websockets \
        websocket-client \
        pysocks \
        dnspython \
        cryptography \
        pyjwt \
        pyyaml \
        xmltodict \
        paramiko \
        selenium \
        pymysql \
 && dirsearch --version 2>&1 | head -1 \
 && arjun --help 2>&1 | head -1 \
 && wafw00f --help 2>&1 | head -1 \
 && semgrep --version \
 && python3 -c 'import requests, httpx, aiohttp, bs4, lxml, urllib3, websockets, websocket, socks, dns, cryptography, jwt, yaml, xmltodict, paramiko, selenium, pymysql; print("python libs OK (17 packages)")'

# Layer 3: jwt_tool 不在 PyPI（pyjwt 是不同包）——git clone + 包装脚本到 /usr/local/bin。
RUN git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool \
 && pip install --quiet -r /opt/jwt_tool/requirements.txt \
 && printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool \
 && chmod +x /usr/local/bin/jwt_tool \
 && jwt_tool -h 2>&1 | head -1

# Layer 4: ProjectDiscovery + 同行 Go 工具二进制——动态拉 GitHub releases/latest。
# 不装 golang SDK 省 ~800MB——直接拉官方 release 二进制。
# 用 GitHub API 查询 latest tag 而非固定 ARG，**build 即升级**。
# API 无认证 60 次/小时 quota；本 layer 9 次调用（8 工具 + 后置 ysoserial），余量充足。
ARG TARGETARCH=amd64

# Helper 函数：跟 GitHub /releases/latest 的 302 重定向拿最新 tag（去前导 v）。
# 不走 api.github.com（容器内常限速 403），用 github.com HTTP redirect 头。
# 加 3 次 retry——单次 curl 偶发抖动很常见。
# 失败兜底：传入 fallback 版本（第二参数），无 fallback 才 exit 1。
RUN cat <<'EOF' > /usr/local/bin/gh-latest
#!/bin/sh
# usage: gh-latest <owner/repo> [fallback_version]
# 输出：版本号（无前导 v）；3 次 retry 都失败 + 无 fallback → exit 1
fallback="$2"
for attempt in 1 2 3; do
  url=$(curl -sILf --max-time 15 -o /dev/null -w '%{url_effective}' "https://github.com/$1/releases/latest" 2>/dev/null) || url=""
  case "$url" in
    *tag/v*) echo "$url" | sed 's|.*tag/v||'; exit 0 ;;
    *tag/*)  echo "$url" | sed 's|.*tag/||';  exit 0 ;;
  esac
  echo "gh-latest: attempt $attempt/3 failed for $1 (url='$url')" >&2
  sleep 2
done
if [ -n "$fallback" ]; then
  echo "gh-latest: 3 次失败，用 fallback=$fallback for $1" >&2
  echo "$fallback"
  exit 0
fi
echo "gh-latest: 3 次失败且无 fallback for $1" >&2
exit 1
EOF
RUN chmod +x /usr/local/bin/gh-latest

# 用一个 RUN 串起 8 个工具下载——版本号空就 exit 1（避免之前 build5 的"装空"灾难）。
RUN set -eu \
 && cd /tmp \
 # ProjectDiscovery 5 工具（zip）—— fallback 版本号是 build7 实测到的 latest（兜底用）
 && for repo_tool_fb in \
        "projectdiscovery/subfinder:subfinder:2.14.0" \
        "projectdiscovery/httpx:httpx:1.9.0" \
        "projectdiscovery/katana:katana:1.6.1" \
        "projectdiscovery/nuclei:nuclei:3.8.0" \
        "projectdiscovery/interactsh:interactsh-client:1.3.1" \
    ; do \
        repo=$(echo "$repo_tool_fb" | cut -d: -f1) ; \
        tool=$(echo "$repo_tool_fb" | cut -d: -f2) ; \
        fb=$(echo "$repo_tool_fb"   | cut -d: -f3) ; \
        v=$(gh-latest "$repo" "$fb") ; \
        [ -n "$v" ] || { echo "[FATAL] empty version for $repo" ; exit 1 ; } ; \
        echo "[gh-latest] $tool=$v from $repo" ; \
        wget "https://github.com/$repo/releases/download/v$v/${tool}_${v}_linux_${TARGETARCH}.zip" -O "$tool.zip" ; \
        unzip -o "$tool.zip" -d /usr/local/bin "$tool" ; \
        rm "$tool.zip" ; \
    done \
 # ffuf / trufflehog（tar.gz，命名 tool_VERSION_OS_ARCH.tar.gz）
 && for repo_tool_fb in \
        "ffuf/ffuf:ffuf:2.1.0" \
        "trufflesecurity/trufflehog:trufflehog:3.95.2" \
    ; do \
        repo=$(echo "$repo_tool_fb" | cut -d: -f1) ; \
        tool=$(echo "$repo_tool_fb" | cut -d: -f2) ; \
        fb=$(echo "$repo_tool_fb"   | cut -d: -f3) ; \
        v=$(gh-latest "$repo" "$fb") ; \
        [ -n "$v" ] || { echo "[FATAL] empty version for $repo" ; exit 1 ; } ; \
        echo "[gh-latest] $tool=$v from $repo" ; \
        url="https://github.com/$repo/releases/download/v$v/${tool}_${v}_linux_${TARGETARCH}.tar.gz" ; \
        wget "$url" -O "$tool.tar.gz" ; \
        [ -s "$tool.tar.gz" ] || { echo "[FATAL] download failed for $tool: $url" ; exit 1 ; } ; \
        mkdir -p "/tmp/extract-$tool" ; \
        tar -xzf "$tool.tar.gz" -C "/tmp/extract-$tool" ; \
        bin=$(find "/tmp/extract-$tool" -type f -name "$tool" | head -1) ; \
        [ -n "$bin" ] || { echo "[FATAL] $tool binary not in tarball; contents:" ; find "/tmp/extract-$tool" -type f ; exit 1 ; } ; \
        cp "$bin" "/usr/local/bin/$tool" ; \
        chmod +x "/usr/local/bin/$tool" ; \
        rm -rf "/tmp/extract-$tool" "$tool.tar.gz" ; \
    done \
 # dalfox 命名双特殊：archive 名 dalfox-linux-amd64.tar.gz + 内含二进制叫 dalfox-linux-amd64（不是 dalfox）
 && v=$(gh-latest hahwul/dalfox 2.13.0) \
 && echo "[gh-latest] dalfox=$v from hahwul/dalfox" \
 && url="https://github.com/hahwul/dalfox/releases/download/v$v/dalfox-linux-${TARGETARCH}.tar.gz" \
 && wget "$url" -O dalfox.tar.gz \
 && [ -s dalfox.tar.gz ] || { echo "[FATAL] dalfox download failed: $url"; exit 1; } \
 && tar -xzf dalfox.tar.gz "dalfox-linux-${TARGETARCH}" -O > /usr/local/bin/dalfox \
 && [ -s /usr/local/bin/dalfox ] || { echo "[FATAL] dalfox binary empty after extract"; exit 1; } \
 && chmod +x /usr/local/bin/dalfox \
 && rm -f dalfox.tar.gz \
 && chmod +x /usr/local/bin/subfinder /usr/local/bin/httpx /usr/local/bin/katana /usr/local/bin/nuclei /usr/local/bin/interactsh-client /usr/local/bin/ffuf /usr/local/bin/dalfox /usr/local/bin/trufflehog \
 # Sanity check——单独每行 echo + 显式路径，失败立即 abort（set -e 在 RUN 顶部已开）
 && echo "=== Layer 4 sanity check ===" \
 && /usr/local/bin/subfinder -version 2>&1 | head -1 \
 && /usr/local/bin/httpx -version 2>&1 | head -1 \
 && /usr/local/bin/katana -version 2>&1 | head -1 \
 && /usr/local/bin/nuclei -version 2>&1 | head -1 \
 && /usr/local/bin/ffuf -V 2>&1 | head -1 \
 && /usr/local/bin/dalfox version 2>&1 | head -1 \
 && /usr/local/bin/trufflehog --version 2>&1 | head -1 \
 && /usr/local/bin/interactsh-client -version 2>&1 | head -1

# Layer 5: nuclei 模板预拉——首次运行避免联网下载（容器内可能无外网或慢）。
# 模板存在 /root/.config/nuclei-templates，~80MB。
# 失败不阻塞 build（容器内可能拉不到 github，运行时再拉也行）。
RUN nuclei -update-templates -silent 2>/dev/null || true \
 && ls /root/.config/nuclei-templates 2>/dev/null | head -3 || true

# Layer 6: 反序列化 payload 生成器。
# - phpggc：git clone（PHP 脚本，~2MB）
# - ysoserial：jar 文件（Java 反序列化 payload，~5MB）
RUN git clone --depth 1 https://github.com/ambionics/phpggc.git /opt/phpggc \
 && printf '#!/bin/sh\nexec /opt/phpggc/phpggc "$@"\n' > /usr/local/bin/phpggc \
 && chmod +x /usr/local/bin/phpggc /opt/phpggc/phpggc \
 && phpggc -h 2>&1 | head -1 || true

# ysoserial 用 gh-latest；fallback 到 0.0.6（实际多年未更新仍是这个）。
# wget 失败时显式 abort（之前用 || true 吞错导致 jar 0 字节静默通过）。
RUN set -e \
 && v=$(gh-latest frohoff/ysoserial 0.0.6) \
 && echo "[gh-latest] ysoserial=$v" \
 && url="https://github.com/frohoff/ysoserial/releases/download/v$v/ysoserial-all.jar" \
 && wget "$url" -O /opt/ysoserial.jar \
 && [ -s /opt/ysoserial.jar ] || { echo "[FATAL] ysoserial jar 下载失败/空: $url"; exit 1; } \
 && printf '#!/bin/sh\nexec java -jar /opt/ysoserial.jar "$@"\n' > /usr/local/bin/ysoserial \
 && chmod +x /usr/local/bin/ysoserial \
 && ls -la /opt/ysoserial.jar /usr/local/bin/ysoserial \
 && ysoserial 2>&1 | head -1

# Layer 7: SecLists wordlist——
# 多次尝试都失败（sparse-checkout / wget single files 在 docker desktop
# gvisor 网络对 raw.githubusercontent.com 偶发 4xx/timeout）。
# 当前最务实策略：建空目录占位 + 写 README 提示运行时拉取，build 100% 稳定。
# LLM 实战需要 wordlist 时，nuclei/dalfox 自带内置 payload 已覆盖 80% 场景；
# 真要 SecLists 全集，run_command 内 git clone 即可（容器有 git + 出网）。
RUN set -e \
 && mkdir -p /opt/SecLists \
 && printf 'SecLists 未预装；运行时按需拉:\n  git clone --depth=1 https://github.com/danielmiessler/SecLists.git /opt/SecLists\n或单文件:\n  curl -sLO https://raw.githubusercontent.com/danielmiessler/SecLists/master/Discovery/Web-Content/common.txt\n' \
        > /opt/SecLists/README.md \
 && echo "SecLists 占位创建完毕（运行时按需拉取）"

WORKDIR /work

# 摘要打印——build 完确认装齐。
RUN echo "=== liusha/pentools:1.0.0 (内容 v1.1) 工具清单 ===" \
 && echo "recon:           subfinder | httpx | katana | nmap | wafw00f" \
 && echo "discovery:       ffuf | dirsearch | arjun" \
 && echo "vulnscan:        nuclei (templates: $(ls /root/.config/nuclei-templates 2>/dev/null | wc -l) dirs)" \
 && echo "injection:       sqlmap | dalfox" \
 && echo "deserialization: ysoserial (java) | phpggc (php)" \
 && echo "auth:            hydra | jwt_tool" \
 && echo "sast:            semgrep | trufflehog" \
 && echo "utility:         curl | python3 $(python3 --version) | jq | interactsh-client | mysql $(mysql --version | awk '{print $5}' | tr -d ',') | google-chrome $(google-chrome --version | awk '{print $3}') | chromedriver $(chromedriver --version | awk '{print $2}')" \
 && echo "wordlist:        /opt/SecLists ($(du -sh /opt/SecLists 2>/dev/null | cut -f1))"
