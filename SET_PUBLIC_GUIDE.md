# 将 GHCR 包设为 Public 的步骤

## 为什么这样做？

经过大量调试，我们发现：
- ✅ 镜像可以被 push
- ✅ manifest 可以被检查
- ✅ 镜像可以被 pre-pull
- ❌ **但 docker build 时使用私有 base 镜像失败**

这是 GitHub Actions + GHCR 私有包 + 大镜像（8-10GB）的已知问题。

## 操作步骤

### 1. 访问 Packages 页面
https://github.com/V3teran?tab=packages

### 2. 找到 pentools-base 包
在包列表中找到 `pentools-base`

### 3. 进入 Package settings
点击包名 → 右上角 "Package settings"

### 4. 修改可见性
- 找到 "Danger Zone"
- 点击 "Change package visibility"
- 选择 "Public"
- 确认

### 5. 重新触发构建
```bash
gh workflow run build-pentools.yml --ref main
```

## 如果成功

构建应该会顺利完成：
- build-base: 成功
- test-base: 成功
- push-base: 成功
- **build-final: 成功**（之前一直失败）
- test-final: 成功
- push-final: 成功

## 如果仍然失败

那说明问题不是权限，需要查看浏览器日志：
https://github.com/V3teran/liusha/actions

## 安全性

开源项目的容器镜像通常都是 public 的，这是正常做法。
如果担心安全，可以在验证构建成功后再改回 private。

