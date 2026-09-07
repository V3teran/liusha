# Pentools 镜像构建问题总结

## 问题现象
- build-base 成功（90个工具构建完成）
- test-base 成功（L1+L2测试全部通过）
- push-base 成功（推送 test-SHA 和 latest tag）
- **build-final 持续失败**（5分钟后，在 "Build final image" 步骤）

## 已验证排除的原因
1. ✅ Dockerfile.final 语法正确（本地测试完全成功）
2. ✅ 镜像存在且可访问（manifest 验证通过）
3. ✅ 镜像可以被 pull（pre-pull 步骤成功）
4. ✅ ARG 作用域正确（已修复 multi-stage build 声明）
5. ✅ 不是缓存问题（禁用缓存仍失败）
6. ✅ 不是 tag 同步问题（使用 test-SHA，预拉取镜像）
7. ✅ 不是等待时间问题（30秒等待，预拉取都尝试过）

## 可能的根本原因

### 1. GHCR 私有包权限问题（最可能）
- 虽然 `docker pull` manifest 成功，但 `docker build` 时拉取层可能失败
- GitHub Actions 的 GITHUB_TOKEN 对私有包的权限可能有限制
- **解决方案**：将 pentools-base 包设为 public

### 2. CI 环境的资源限制
- GitHub Actions runner 的网络带宽/存储限制
- 8-10GB 大镜像在 CI 中处理困难
- **解决方案**：拆分镜像，或使用自托管 runner

### 3. Docker buildx 与 GHCR 的交互问题
- buildx 在处理大型私有镜像时可能有 bug
- 特别是在 FROM 指令中引用 GHCR 私有镜像
- **解决方案**：升级 docker/build-push-action 版本

## 推荐解决方案

### 方案 1：将 GHCR 包设为 Public（最简单）

1. 访问 https://github.com/V3teran?tab=packages
2. 找到 `pentools-base` 包
3. 点击进入 Package settings
4. 将 Visibility 改为 **Public**
5. 保存更改
6. 重新触发 CI：
   ```bash
   gh workflow run build-pentools.yml --ref main
   ```

### 方案 2：查看实际构建日志定位问题

1. 访问最新失败的 run：https://github.com/V3teran/liusha/actions/runs/34077498621
2. 展开 `build-final` job
3. 点击 "Build final image (push to test tag)" 步骤
4. 查看完整的构建输出
5. 搜索 `ERROR` 或 `failed` 找到具体错误
6. 根据错误信息调整 workflow

### 方案 3：本地手动构建并推送（临时方案）

```bash
cd /Users/Xlbula/workspace/programs/go/liusha

# 1. 登录 GHCR（需要有 packages:write 权限的 token）
echo $GITHUB_TOKEN | docker login ghcr.io -u V3teran --password-stdin

# 2. 构建 base 镜像（约 45 分钟）
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

## 已完成的工作

### Dockerfile 修复
- ✅ 修复了 multi-stage build 中的 ARG 作用域问题
- ✅ 全局声明 BASE_IMAGE ARG（第 13 行）
- ✅ 在 stage 1 重新声明（第 27 行）
- ✅ 本地测试完全成功

### Workflow 优化
- ✅ 添加了镜像验证步骤（manifest 检查）
- ✅ 添加了预拉取步骤（预热镜像层）
- ✅ 使用 test-SHA tag（避免 latest 同步延迟）
- ✅ 添加了 30 秒等待（给 GHCR 同步时间）
- ✅ 禁用了可能损坏的缓存
- ✅ 增加了 15 分钟超时

### 测试验证
- ✅ 90 个工具全部测试通过（L1 命令存在，L2 版本验证）
- ✅ Base 镜像构建和推送成功
- ✅ 本地 Dockerfile.final 构建成功

## 技术细节

### 镜像大小
- pentools-base: ~8-10 GB（Kali + 90 个工具）
- pentools-final: ~8-10 GB（base + sandbox-server ~20MB）

### CI 运行时间
- build-base: ~35-40 分钟
- test-base: ~10-15 分钟
- build-final: 应该 ~5 分钟（但持续失败）

### 相关文件
- `.github/workflows/build-pentools.yml` - CI workflow
- `deployments/tool-images/pentools/Dockerfile.base` - Base 镜像
- `deployments/tool-images/pentools/Dockerfile.final` - Final 镜像
- `deployments/tool-images/pentools/tools.yaml` - 工具清单

## 联系方式

如果需要协助：
1. 查看 GitHub Actions 的完整日志
2. 检查 GHCR 包的权限设置
3. 考虑将包设为 public（开源项目推荐）

## 最后更新

- 日期：2026-09-07
- 状态：build-final 持续失败，等待设置 public 或查看日志
- 下一步：方案 1（设为 public）是最快的解决方案
