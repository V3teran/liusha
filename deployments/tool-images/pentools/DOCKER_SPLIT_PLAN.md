# Pentools 镜像两层拆分方案

## 📊 背景

当前 pentools 镜像是单层构建，每次变更（sandbox-server 代码、browser-use 脚本等）都需要重新构建整个镜像（~40 分钟）。

**问题**：
- sandbox-server 代码变更频繁
- browser-use 脚本经常调整
- 但安全工具（nmap、sqlmap、nuclei 等）几乎不变

**目标**：拆分为两层镜像，利用 Docker 缓存，加速 99% 的构建场景。

---

## 🎯 拆分策略

### **Layer 1：pentools-base（不常变动）**
包含所有安全工具和运行时环境（~40 分钟构建）

### **Layer 2：pentools（频繁变动）**
只包含 liusha 特有组件（~2-3 分钟构建）

---

## 📦 Base 镜像内容（Layer 1-11）

**构建时间**：首次 ~40 分钟，后续完全缓存

### 包含内容：

#### 1. 基础依赖（Layer 1）
- ca-certificates、curl、wget、git、unzip、tar
- Python 3 + pip + venv
- Node.js + npm
- PHP CLI
- Golang + Maven
- MySQL client
- 编译工具链（gcc、libssl-dev、libffi-dev）
- yq

#### 2. 双 JDK（Layer 1b）
- JDK 8 (8u352-b08) → /opt/jdk8（默认）
- JDK 17 (17.0.19+10) → /opt/jdk17
- java8/java17/javac8/javac17 切换脚本

#### 3. Kali apt 工具（Layer 2）
```
nmap, sqlmap, hydra, ffuf, feroxbuster, wafw00f, arjun,
subfinder, httpx-toolkit, nuclei, phpggc, trufflehog, seclists
```

#### 4. PD 工具 wrapper（Layer 3）
- httpx（httpx-toolkit → httpx）
- nuclei、subfinder（添加 -duc 标志）

#### 5. Release binary（Layer 4）
- katana v1.6.1
- gau v2.2.4
- interactsh-client v1.3.1

#### 6. pip/npm 工具（Layer 5）
- semgrep（pip）
- spectral（npm）

#### 7. ruby + pipx 基座（Layer 5b）
- ruby-full（供 gem 系工具）
- pipx（供 prowler、pacu 等云工具）

#### 8. 专属安装层（Layer 6）
- jwt_tool（git clone）
- dalfox v3.1.2（musl 静态版）
- ysoserial v0.0.6（fat jar）
- ysomap（git + maven 编译）

#### 9. Injection 工具（Layer 6b）
- SSTImap（git + requirements.txt）
- SSRFmap（git + pyproject 依赖）

#### 10. gdb + pwndbg（Layer 6c）
- gdb（apt）
- pwndbg（git + setup.sh）

#### 11. Cloud/Container 工具（Layer 6d）
```
trivy v0.73.0, kubectl v1.36.3, peirates v1.29a,
kube-bench v0.16.0, ligolo-ng v0.9.1, pwninit 3.3.3, cloudfox v2.0.5
```

#### 12. 重依赖 Python 工具（独立 venv，Layer 6e）
- volatility3（venv → /opt/venv-vol）
- angr（venv → /opt/venv-angr）
- RsaCtfTool（venv → /opt/venv-rsactf）

#### 13. stegoveritas + 依赖（Layer 6f）
- stegoveritas（pip + install_deps）

#### 14. manifest 驱动安装器（Layer 8）
通过 `tools.yaml` + `install-from-manifest.sh` 安装：
```
binwalk, fcrackzip, hashcat, john, patchelf, proxychains4,
responder, ROPgadget, steghide, stegseek, strace, testdisk, tshark,
hash-identifier, jq, impacket-secretsdump, metasploit-framework,
afl++, ghidra, radare2, commix, netexec, bloodhound, certipy, pacu,
prowler, cloudsplaining, evil-winrm, zsteg, one_gadget, seccomp-tools
（共 78 个工具）
```

#### 15. nuclei 模板预拉（Layer 9）
- git clone nuclei-templates → /root/nuclei-templates

#### 16. browser-use venv（Layer 10）
- Python 3.12 独立 venv → /opt/browser-use-venv
- browser-use[cli] + playwright + httpx
- playwright chromium 预装
- chromium 软链接 → /usr/local/bin/chromium

#### 17. Python 常用库（Layer 11）
```
requests, httpx, aiohttp, beautifulsoup4, lxml, urllib3,
websockets, websocket-client, pysocks, dnspython, cryptography,
pyjwt, pyyaml, xmltodict, paramiko, pymysql,
pwntools, pycryptodome
```

---

## 🚀 Final 镜像内容（Layer 7 + 12-13）

**构建时间**：~2-3 分钟

### 包含内容：

#### 1. Stage 0（Go builder）
- 编译 sandbox-server 二进制

#### 2. browser-use 薄客户端（Layer 7）
- `browser-use` 脚本
- `browser-svc.py` 服务端

#### 3. entrypoint（Layer 12）
```sh
#!/bin/sh
exec /usr/local/bin/sandbox-server
```

#### 4. sandbox-server 二进制（Layer 13）
- COPY from Stage 0
- 设置可执行权限

---

## 📝 Dockerfile.base 示例

```dockerfile
# syntax=docker/dockerfile:1.7
# pentools-base：安全工具基础镜像（不常变动）

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

# apt 重试
RUN printf 'Acquire::Retries "5";\nAcquire::http::Timeout "120";\nAcquire::https::Timeout "120";\n' \
      > /etc/apt/apt.conf.d/80-retries

# ── Layer 1-11（从原 Dockerfile 58-392 行复制）──
# ... 完整复制 ...

# 验证关键工具
RUN echo "=== pentools-base 构建完成 ===" \
 && nmap --version | head -1 \
 && sqlmap --version | head -1 \
 && nuclei -version | head -1 \
 && browser-use-cli --version | head -1 \
 && java -version 2>&1 | head -1 \
 && chromium --version | head -1

WORKDIR /work
```

---

## 📝 Dockerfile 示例（最终镜像）

```dockerfile
# syntax=docker/dockerfile:1.7
# pentools：liusha 沙箱镜像（基于 pentools-base）

# ── Stage 0: 编译 sandbox-server ─────────────────────────────────────────────
FROM golang:1.25-alpine AS sandbox-builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/sandbox-server cmd/sandbox-server
COPY internal/sandbox internal/sandbox
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/sandbox-server ./cmd/sandbox-server

# ── Stage 1: 基于 base 镜像构建 ──────────────────────────────────────────────
FROM ghcr.io/your-org/pentools-base:latest

# Layer 7: browser-use 薄客户端
COPY deployments/tool-images/pentools/browser-use /usr/local/bin/browser-use
COPY deployments/tool-images/pentools/browser-svc.py /usr/local/bin/browser-svc.py
RUN chmod +x /usr/local/bin/browser-use /usr/local/bin/browser-svc.py

# Layer 12: entrypoint
RUN printf '%s\n' \
  '#!/bin/sh' \
  'exec /usr/local/bin/sandbox-server' \
  > /usr/local/bin/sandbox-entrypoint.sh \
 && chmod +x /usr/local/bin/sandbox-entrypoint.sh

# Layer 13: sandbox-server 二进制
COPY --from=sandbox-builder /out/sandbox-server /usr/local/bin/sandbox-server
RUN chmod +x /usr/local/bin/sandbox-server && test -x /usr/local/bin/sandbox-server

WORKDIR /work
EXPOSE 8080
CMD ["/usr/local/bin/sandbox-entrypoint.sh"]
```

---

## 🔄 GitHub Actions CI 配置

```yaml
# .github/workflows/build-pentools.yml
name: Build Pentools Images

on:
  push:
    branches: [main]
    paths:
      - 'deployments/tool-images/pentools/**'
      - 'cmd/sandbox-server/**'
      - 'internal/sandbox/**'
  workflow_dispatch:
    inputs:
      rebuild_base:
        description: '是否重建基础镜像'
        required: false
        type: boolean
        default: false

env:
  REGISTRY: ghcr.io
  BASE_IMAGE: ghcr.io/${{ github.repository_owner }}/pentools-base
  FINAL_IMAGE: ghcr.io/${{ github.repository_owner }}/pentools

jobs:
  build-base:
    # 仅当 tools.yaml 或 Dockerfile.base 变更时，或手动触发时才构建
    if: |
      github.event_name == 'workflow_dispatch' && inputs.rebuild_base == true ||
      contains(github.event.head_commit.modified, 'tools.yaml') ||
      contains(github.event.head_commit.modified, 'Dockerfile.base')
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3
      
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      
      - name: Build and push base image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: deployments/tool-images/pentools/Dockerfile.base
          push: true
          tags: |
            ${{ env.BASE_IMAGE }}:latest
            ${{ env.BASE_IMAGE }}:${{ github.sha }}
          cache-from: type=registry,ref=${{ env.BASE_IMAGE }}:buildcache
          cache-to: type=registry,ref=${{ env.BASE_IMAGE }}:buildcache,mode=max

  build-final:
    needs: [build-base]
    # 如果 build-base 跳过，也继续构建（使用已有的 base）
    if: always()
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3
      
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      
      - name: Build and push final image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: deployments/tool-images/pentools/Dockerfile
          push: true
          tags: |
            ${{ env.FINAL_IMAGE }}:latest
            ${{ env.FINAL_IMAGE }}:${{ github.sha }}
          cache-from: type=registry,ref=${{ env.FINAL_IMAGE }}:buildcache
          cache-to: type=registry,ref=${{ env.FINAL_IMAGE }}:buildcache,mode=max
```

---

## 📊 性能对比

| 变更场景 | 当前单镜像 | 拆分后 | 提升倍数 |
|---------|-----------|--------|----------|
| **sandbox-server 代码变更** | ~40 分钟 | ~2 分钟 | **20x** |
| **browser-use 脚本变更** | ~40 分钟 | ~2 分钟 | **20x** |
| **browser-svc.py 变更** | ~40 分钟 | ~2 分钟 | **20x** |
| **tools.yaml 变更** | ~40 分钟 | ~40 分钟 | 1x（罕见）|
| **CI 缓存命中率** | 低（Layer 1-11 反复构建） | 高（base 完全缓存） | - |
| **本地开发迭代** | 每次 40 分钟 | 首次 42 分钟，后续 2 分钟 | **20x** |

---

## 🎯 实施步骤

### 1. 创建 Dockerfile.base
```bash
# 复制 Layer 1-11（58-392 行）到新文件
cp deployments/tool-images/pentools/Dockerfile deployments/tool-images/pentools/Dockerfile.base
# 编辑 Dockerfile.base，只保留 Layer 1-11
```

### 2. 修改 Dockerfile（最终镜像）
```bash
# 保留 Stage 0 + Layer 7 + Layer 12-13
# FROM 改为 FROM ghcr.io/your-org/pentools-base:latest
```

### 3. 本地测试构建
```bash
# 构建 base
docker build -f deployments/tool-images/pentools/Dockerfile.base \
  -t pentools-base:test .

# 构建 final
docker build -f deployments/tool-images/pentools/Dockerfile \
  -t pentools:test .

# 验证工具完整性
docker run --rm pentools:test nmap --version
docker run --rm pentools:test /usr/local/bin/sandbox-server --version
```

### 4. 配置 CI
```bash
# 创建 .github/workflows/build-pentools.yml
# 首次运行会构建两个镜像
# 后续只构建 final 镜像（base 从缓存加载）
```

### 5. 更新部署配置
```yaml
# k8s/deployment.yaml 或 docker-compose.yml
image: ghcr.io/your-org/pentools:latest
```

---

## ⚠️ 注意事项

1. **Base 镜像版本管理**
   - 使用 `latest` tag 和 commit SHA tag
   - tools.yaml 变更时需手动触发 base 重建

2. **缓存失效策略**
   - Base 镜像：tools.yaml 或 Dockerfile.base 变更时重建
   - Final 镜像：每次 push 都构建（利用 base 缓存）

3. **镜像大小**
   - Base 镜像：~5-6GB（包含所有工具）
   - Final 镜像：~5-6GB + sandbox-server（~20MB）
   - 总空间占用与单镜像相同，但构建速度大幅提升

4. **回退方案**
   - 保留原 Dockerfile 作为 Dockerfile.monolithic
   - 如果拆分后出现问题，可以快速回退

---

## 🔍 验证清单

拆分完成后验证：

- [ ] base 镜像包含所有 78 个工具
- [ ] final 镜像包含 sandbox-server
- [ ] final 镜像包含 browser-use/browser-svc.py
- [ ] 容器能正常启动（sandbox-server 监听 8080）
- [ ] browser-use 流量捕获正常工作
- [ ] CI 构建 base 镜像成功（首次）
- [ ] CI 构建 final 镜像成功（利用 base 缓存）
- [ ] 本地测试 sandbox-server 功能正常
