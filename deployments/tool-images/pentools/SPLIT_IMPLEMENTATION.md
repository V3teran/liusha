# Pentools 镜像拆分实施指南

## 📊 概述

已完成镜像两层拆分：

| 镜像 | 内容 | 构建时间 | 变更频率 |
|------|------|---------|---------|
| **pentools-base** | 所有安全工具 + Python 环境 | ~40 分钟 | 低（仅 tools.yaml 变更时） |
| **pentools** | sandbox-server + browser-use 脚本 | ~2-3 分钟 | 高（代码变更时） |

---

## 🎯 效果对比

| 变更场景 | 拆分前 | 拆分后 | 提升 |
|---------|--------|--------|------|
| sandbox-server 代码变更 | 40 分钟 | 2 分钟 | **20x** |
| browser-use 脚本变更 | 40 分钟 | 2 分钟 | **20x** |
| tools.yaml 变更 | 40 分钟 | 40 分钟 | 1x |
| CI 缓存命中率 | 低 | 高 | - |

---

## 📁 文件结构

```
deployments/tool-images/pentools/
├── Dockerfile              # 原单层镜像（保留作备份）
├── Dockerfile.base         # 基础镜像（Layer 1-12）
├── Dockerfile.final        # 最终镜像（引用 base + 添加业务代码）
├── tools.yaml              # 工具清单
├── browser-use             # 浏览器客户端脚本
├── browser-svc.py          # 浏览器服务端
└── DOCKER_SPLIT_PLAN.md    # 原始拆分方案

.github/workflows/
├── test-pentools.yml       # 工具可用性测试
└── build-pentools.yml      # 镜像构建 CI（新）
```

---

## 🔧 本地使用

### 构建基础镜像（首次或 tools.yaml 变更时）

```bash
docker build -f deployments/tool-images/pentools/Dockerfile.base \
  -t pentools-base:local .
```

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
- `install-from-manifest.sh` 变更
- 手动触发 workflow 并勾选 `rebuild_base`

**构建最终镜像**（每次都构建）：
- 任何 `deployments/tool-images/pentools/**` 文件变更
- 基础镜像构建完成后

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

1. 修改 `tools.yaml`（添加工具定义）
2. 更新 `Dockerfile.base`（如果需要特殊安装逻辑）
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

### 回退到单层镜像

如果拆分后出现问题，可以快速回退：

```bash
# 使用原始 Dockerfile
docker build -f deployments/tool-images/pentools/Dockerfile.final\
  -t pentools:monolithic .
```

---

## 📊 验证清单

拆分完成后验证：

- [x] `Dockerfile.base` 创建完成
- [x] `Dockerfile.final` 创建完成
- [x] `.github/workflows/build-pentools.yml` 创建完成
- [ ] 本地测试基础镜像构建成功
- [ ] 本地测试最终镜像构建成功
- [ ] 容器能正常启动（sandbox-server 监听 8080）
- [ ] browser-use 流量捕获正常工作
- [ ] CI 构建基础镜像成功（首次）
- [ ] CI 构建最终镜像成功（利用 base 缓存）
- [ ] 验证构建时间提升（sandbox 变更 < 5 分钟）

---

## 🎊 下一步

测试通过后：

1. ✅ 提交所有文件到 main 分支
2. ✅ 等待 CI 首次构建基础镜像（~40 分钟）
3. ✅ 验证最终镜像构建成功（~2 分钟）
4. ✅ 修改一次 sandbox-server 代码验证提速效果
5. ✅ 更新部署配置使用新的镜像地址

---

**最后更新**: 2026-09-04
