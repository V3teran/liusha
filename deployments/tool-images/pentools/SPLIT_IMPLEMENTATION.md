# Pentools 镜像拆分实施指南

## 📊 概述

已完成镜像两层拆分：

| 镜像 | 内容 | 构建时间 | 变更频率 |
|------|------|---------|---------|
| **pentools-base** | 所有安全工具（90 个）+ Python 环境 | ~40 分钟 | 低（仅 tools.yaml 或 Dockerfile.base 变更时） |
| **pentools** | sandbox-server + browser-use 脚本 | ~2-3 分钟 | 高（代码变更时） |

---

## 🎯 效果对比

| 变更场景 | 拆分前 | 拆分后 | 提升 |
|---------|--------|--------|------|
| sandbox-server 代码变更 | 40 分钟 | 2 分钟 | **20x** |
| browser-use 脚本变更 | 40 分钟 | 2 分钟 | **20x** |
| tools.yaml 变更 | 40 分钟 | 40 分钟 | 1x |
| Dockerfile.base 变更 | 40 分钟 | 40 分钟 | 1x |
| CI 缓存命中率 | 低 | 高 | - |

---

## 📁 文件结构

```
deployments/tool-images/pentools/
├── Dockerfile.base         # 基础镜像（31 层，90 个工具）⭐
├── Dockerfile.final        # 最终镜像（引用 base + 添加业务代码）⭐
├── Dockerfile.base.old     # 原单层镜像备份
├── tools.yaml              # 工具清单（仅 name/category/description）
├── browser-use             # 浏览器客户端脚本
├── browser-svc.py          # 浏览器服务端
└── SPLIT_IMPLEMENTATION.md # 本文档

.github/workflows/
├── ci.yml                  # 主 CI（vet + test + build）
└── build-pentools.yml      # Pentools 专用构建 CI
```

---

## 🏗️ 架构说明

### Dockerfile.base（568 行，31 层）

所有工具安装逻辑全在 Dockerfile.base 内，**不依赖外部脚本**：

- **Layer 1**: 基础环境（Python/Node/Ruby/Go/Maven/yq）
- **Layer 2**: 双 JDK（Temurin 8 + 17）
- **Layer 3**: apt 工具（48 个：nmap, sqlmap, nuclei 等）
- **Layer 4**: httpx 命名兼容 + PD 系工具 -duc wrapper
- **Layer 5**: pip 工具（2 个：semgrep, ROPgadget）
- **Layer 6**: pipx 工具（7 个：prowler, pacu, checkov 等）
- **Layer 7**: npm 工具（1 个：spectral）
- **Layer 8**: gem 工具（4 个：one_gadget, evil-winrm 等）
- **Layer 9**: release 工具（5 个：katana, fscan, gau 等）
- **Layer 10**: release-bin 工具（2 个：chisel, linpeas）
- **Layer 11**: git 工具（1 个：paramspider）
- **Layer 12-30**: 复杂工具（19 个，每个独立层）
  - ysomap, ysoserial, jwt_tool, dalfox
  - SSTImap, SSRFmap, RsaCtfTool
  - angr, browser-use, stegoveritas, vol
  - peirates, kube-bench, kubectl
  - ligolo-ng, pwninit, cloudfox, trivy
  - gdb + pwndbg
- **Layer 31**: nuclei 模板预拉（~10k+ 模板）

**每层强制验证**：
- `set -eux`：任何命令失败立即退出
- `command -v <tool>`：验证工具可调用
- `<tool> --version`：验证工具能运行

### tools.yaml（简化版）

**只保留三字段**：
- `name`：工具命令名（必须与实际可调用命令一致）
- `category`：能力轴（recon/discovery/vulnscan/injection 等）
- `description`：功能说明（供 agent SystemPrompt 渲染）

**不再包含**：
- `install` 字段：全删除，安装逻辑全在 Dockerfile.base
- 安装方法、包名、版本：全由 Dockerfile.base 管理

### Dockerfile.final

引用 `pentools-base` 作为基础镜像，只添加：
- sandbox-server 二进制
- browser-use 脚本（browser-svc.py）
- 运行时配置

---

## 🔧 本地使用

### 构建基础镜像（首次或 tools.yaml/Dockerfile.base 变更时）

```bash
docker build -f deployments/tool-images/pentools/Dockerfile.base \
  -t pentools-base:local .
```

**预计时间**：~40 分钟（取决于网络和 CPU）

### 构建最终镜像

```bash
# 使用本地 base
docker build -f deployments/tool-images/pentools/Dockerfile.final \
  --build-arg BASE_IMAGE=pentools-base:local \
  -t pentools:local .

# 或使用 GHCR base（如果已推送）
docker build -f deployments/tool-images/pentools/Dockerfile.final \
  -t pentools:local .
```

**预计时间**：~2-3 分钟

### 运行

```bash
docker run --rm -p 8080:8080 pentools:local
```

---

## 🚀 CI/CD 工作流

### 自动触发规则

**构建基础镜像**（满足任一条件）：
- `tools.yaml` 变更
- `Dockerfile.base` 变更
- 手动触发 workflow 并勾选 `rebuild_base`

**构建最终镜像**（每次都构建）：
- 任何 `deployments/tool-images/pentools/**` 文件变更
- `cmd/sandbox-server/**` 或 `internal/sandbox/**` 变更
- 基础镜像构建完成后

### CI 流程（build-pentools.yml）

完整的 **build → test → push** 流程：

```
1. check-base-changed  # 检测是否需要重建 base
   ↓
2. build-base          # 构建 base 镜像（load 到本地）
   ↓
3. test-base           # 测试 base 镜像
   ├─ L1: 命令存在性（90 工具，command -v）
   ├─ L2: 版本输出验证（90 工具，--version）
   └─ L3: 关键工具冒烟测试（6 工具，功能验证）
   ↓
4. push-base           # 测试通过后推送 base
   ↓
5. build-final         # 构建 final 镜像（load 到本地）
   ↓
6. test-final          # 测试 final 镜像
   ├─ sandbox-server 启动验证
   ├─ 健康检查端点验证
   ├─ browser-use 脚本验证
   └─ 工具继承验证（抽样 10 个）
   ↓
7. push-final          # 测试通过后推送 final
```

**关键特性**：
- ✅ 测试失败自动阻断推送
- ✅ 所有构建、测试在 CI runner 完成
- ✅ 本地只需推送代码，CI 自动完成后续
- ✅ 基础镜像和最终镜像独立测试
- ✅ 90 个工具全量验证

### 手动触发

```bash
# 强制重建基础镜像
gh workflow run build-pentools.yml -f rebuild_base=true

# 查看构建状态
gh run list --workflow=build-pentools.yml --limit 5
```

---

## 📦 镜像发布

镜像自动推送到 GitHub Container Registry：

- `ghcr.io/v3teran/pentools-base:latest`
- `ghcr.io/v3teran/pentools-base:<commit-sha>`
- `ghcr.io/v3teran/pentools:latest`
- `ghcr.io/v3teran/pentools:<commit-sha>`

### 拉取镜像

```bash
# 拉取最终镜像（生产使用）
docker pull ghcr.io/v3teran/pentools:latest

# 拉取基础镜像（本地开发）
docker pull ghcr.io/v3teran/pentools-base:latest
```

---

## 🔄 更新工作流

### 场景 1：添加新工具

1. 修改 `tools.yaml`（添加 name/category/description）
2. 修改 `Dockerfile.base`（添加安装逻辑 + 验证命令）
3. 推送到 main 分支
4. CI 自动重建基础镜像（~40 分钟）
5. CI 自动重建最终镜像（~2 分钟）

### 场景 2：修改 sandbox-server 代码

1. 修改 `cmd/sandbox-server/**` 或 `internal/sandbox/**`
2. 推送到 main 分支
3. CI 跳过基础镜像（缓存命中）
4. CI 仅重建最终镜像（~2 分钟）✅

### 场景 3：修改 browser-use 脚本

1. 修改 `browser-use` 或 `browser-svc.py`
2. 推送到 main 分支
3. CI 跳过基础镜像（缓存命中）
4. CI 仅重建最终镜像（~2 分钟）✅

### 场景 4：更新工具版本

1. 修改 `Dockerfile.base`（更新 wget URL 或版本号）
2. 推送到 main 分支
3. CI 自动重建基础镜像（~40 分钟）
4. CI 自动重建最终镜像（~2 分钟）

---

## ⚠️ 注意事项

### 基础镜像更新后

基础镜像更新后，最终镜像会自动使用 `latest` tag 重建。如果需要固定版本：

```dockerfile
# Dockerfile.final 中固定 commit SHA
FROM ghcr.io/v3teran/pentools-base:abc1234
```

### 本地开发

本地开发时，需要先构建或拉取基础镜像：

```bash
# 选项 1: 拉取远程基础镜像
docker pull ghcr.io/v3teran/pentools-base:latest
docker tag ghcr.io/v3teran/pentools-base:latest pentools-base:local

# 选项 2: 本地构建基础镜像（首次构建慢）
docker build -f deployments/tool-images/pentools/Dockerfile.base \
  -t pentools-base:local .
```

### 工具验证失败

如果构建失败，检查：
1. 工具的下载 URL 是否有效（GitHub release 链接可能变更）
2. 工具的版本是否存在
3. 工具的 `--version` 命令是否正确
4. 工具的依赖是否已安装

---

## 📊 验证清单

拆分完成后验证：

- [x] `Dockerfile.base` 创建完成（568 行，31 层）
- [x] `Dockerfile.final` 创建完成
- [x] `tools.yaml` 简化完成（删除所有 install 字段）
- [x] `scripts/build/install-from-manifest.sh` 已删除
- [x] `.github/workflows/build-pentools.yml` 创建完成
- [ ] 本地测试基础镜像构建成功
- [ ] 本地测试最终镜像构建成功
- [ ] 容器能正常启动（sandbox-server 监听 8080）
- [ ] browser-use 流量捕获正常工作
- [ ] CI 构建基础镜像成功（首次）
- [ ] CI 构建最终镜像成功（利用 base 缓存）
- [ ] 验证构建时间提升（sandbox 变更 < 5 分钟）

---

## 🎊 架构优势

### 与原方案对比

| 方案 | 安装逻辑 | 验证方式 | 失败处理 | 维护性 |
|------|---------|---------|---------|--------|
| **原方案**（install-from-manifest.sh） | Bash 脚本解析 YAML | 事后测试 | 静默跳过（noop） | 双源维护 |
| **新方案**（Dockerfile 全权负责） | Dockerfile 原生 RUN | 每层强制验证 | 立即失败退出 | 单一真相源 |

### 核心改进

✅ **单一真相源**：Dockerfile.base 就是唯一的安装脚本
✅ **强制验证**：每层装完必须能调用，失败立即退出
✅ **失败点清晰**：构建失败 = 工具装不上，不再有静默跳过
✅ **简化维护**：tools.yaml 只做声明，不管安装细节
✅ **无隐藏逻辑**：不再有 Bash 脚本解析 YAML 的复杂度

---

**最后更新**: 2025-01-XX（架构简化版）
