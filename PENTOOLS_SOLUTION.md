# Pentools 构建问题 - 最终总结和解决方案

## 问题现状

经过大量调试，我们发现：
- ✅ base 镜像构建成功（90个工具，测试通过）
- ✅ base 镜像推送成功
- ❌ **build-final 持续失败**（即使激进磁盘清理后运行12分钟仍失败）

## 根本原因

**GitHub Actions 标准 runner 无法处理 8-10GB 大镜像的 multi-stage build**

即使：
- 删除了所有预装工具（释放 30-40GB）
- 预拉取了 base 镜像
- 增加了超时时间
- 禁用了缓存

仍然在 10-15 分钟后失败，日志显示 "No space left on device"。

## 推荐解决方案

### 方案 1：本地构建并推送（最快，推荐）

在你的本地 Mac（M1/M2）上构建并推送：

```bash
cd ~/workspace/programs/go/liusha

# 1. 登录 GHCR（需要有 packages:write 权限的 token）
echo $GITHUB_TOKEN | docker login ghcr.io -u V3teran --password-stdin

# 2. 构建 base 镜像（约 45 分钟，只需一次）
cd deployments/tool-images/pentools
docker buildx build --platform linux/amd64 \
  -t ghcr.io/v3teran/pentools-base:latest \
  -f Dockerfile.base --push .

# 3. 构建 final 镜像（约 5 分钟）
cd ../../..
docker buildx build --platform linux/amd64 \
  --build-arg BASE_IMAGE=ghcr.io/v3teran/pentools-base:latest \
  -t ghcr.io/v3teran/pentools:latest \
  -f deployments/tool-images/pentools/Dockerfile.final --push .
```

**优点**：
- ✅ 立即可用，无需修改代码
- ✅ 本地磁盘足够（你的 Mac 有充足空间）
- ✅ 可以看到完整构建过程
- ✅ 成功后可以验证 CI 其他部分

**缺点**：
- 首次构建需要 45-50 分钟
- 需要手动触发

---

### 方案 2：使用 GitHub larger runner（需付费）

修改 `.github/workflows/build-pentools.yml`：

```yaml
build-final:
  runs-on: ubuntu-24.04-4-cores  # 或 ubuntu-24.04-8-cores
```

GitHub larger runners 有更多磁盘空间和资源。

**优点**：
- ✅ 自动化 CI
- ✅ 更多资源（CPU、内存、磁盘）

**缺点**：
- ❌ 需要付费
- ❌ 需要在 GitHub 组织设置中启用

---

### 方案 3：拆分 base 镜像（工程量大）

将 90 个工具拆分成多个小镜像，减小单个镜像大小。

**优点**：
- ✅ 可以在标准 runner 运行
- ✅ 模块化设计

**缺点**：
- ❌ 需要大量重构
- ❌ 复杂度增加

---

### 方案 4：将 GHCR 包设为 Public（辅助）

虽然这不是根本解决方案，但可以：
- 简化权限问题
- 便于调试

访问：https://github.com/V3teran?tab=packages

---

## 我的建议

**立即使用方案 1**（本地构建并推送）：

1. 这样可以立即解除阻塞，让镜像可用
2. 之后可以慢慢优化 CI（方案 2 或 3）
3. 本地构建一次后，CI 只需处理增量更新

---

## 需要的 GitHub Token

创建一个有 `write:packages` 权限的 token：
1. 访问 https://github.com/settings/tokens
2. Generate new token (classic)
3. 勾选 `write:packages`
4. 复制 token，设置环境变量：
   ```bash
   export GITHUB_TOKEN=ghp_your_token_here
   ```

---

## 验证

本地构建成功后，可以验证：
```bash
docker pull ghcr.io/v3teran/pentools:latest
docker run --rm ghcr.io/v3teran/pentools:latest echo "Success"
```

---

## 后续优化

一旦本地推送成功，可以：
1. 修改 CI 为仅在 base 改变时重建
2. 或者考虑使用 larger runner
3. 或者接受本地构建的工作流

---

**最后更新**：2026-09-07
**状态**：GitHub Actions 标准 runner 无法完成构建
**下一步**：本地构建并推送
