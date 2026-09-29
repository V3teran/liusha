# CI 配置更新说明

## 问题
由于 OAuth token 缺少 `workflow` scope，无法通过 API 推送 workflow 文件的修改。

## 解决方案

### 方法1：手动更新（推荐）

1. 在 GitHub Web 界面编辑 `.github/workflows/ci.yml`
2. 在 `jobs:` 部分的开头添加以下内容：

```yaml
jobs:
  lint:
    # 代码质量检查：golangci-lint 包含多个静态分析工具
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: 'stable'
          cache: true
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
          args: --timeout=5m

  test:
    # 运行 vet/gofmt/单元+集成测试：集成用例里的 testcontainers 会在 ubuntu-latest 上拉起真实 pg17。
    runs-on: ubuntu-latest
    needs: lint  # 添加这一行，确保 lint 通过后才运行测试
```

3. 将原来的 `test:` job 中的 `runs-on: ubuntu-latest` 改为 `needs: lint` + `runs-on: ubuntu-latest`

### 方法2：使用具有 workflow 权限的 token

1. 在 GitHub Settings → Developer settings → Personal access tokens
2. 创建新 token，勾选 `workflow` scope
3. 使用新 token 推送

### 方法3：使用本地准备好的配置

本地已经准备好完整的 CI 配置文件草稿，路径：
- 本地工作目录中已有修改（未提交）

可以通过以下命令查看差异：
```bash
git diff .github/workflows/ci.yml
```

## 已添加的 lint job 内容

```yaml
lint:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: 'stable'
        cache: true
    - name: golangci-lint
      uses: golangci/golangci-lint-action@v6
      with:
        version: latest
        args: --timeout=5m
```

## 预期效果

配置后，每次 push 或 PR 时，CI 流程将变为：
1. **lint** - 运行 golangci-lint 检查代码质量
2. **test** - 运行所有测试（依赖 lint 通过）
3. **build** - 构建 Docker 镜像（依赖 test 通过）

这样可以在早期发现代码质量问题，节省 CI 时间。
