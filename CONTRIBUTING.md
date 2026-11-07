# 提交信息约定

格式遵循 Conventional Commits（`type(scope): 主题`），语言与产品受众一致用中文。
机器可读是这套格式的全部价值：将来接 commitlint（格式校验）与
release-please（自动 changelog + 版本号）时零迁移成本。

## 三条纪律

### 1. 主题行 ≤72 显示列（中文按 2 列）

主题 = `type(scope): 做什么 —— 最关键的一个为什么`，`——` 后只留一个短语；
第二、第三个解释降级进正文。列表页超宽截断的提交等于没写主题。

```
# 过长（解释性内容进了主题，显示 80+ 列被截断）
feat(attachment): 会话附件全链——上传暂存落位/落位改写/内联/来源校验

# 收紧（——后一个短语，其余进正文）
feat(attachment): 会话附件全链——消息只存引用，文件单点落位
```

### 2. 正文第一段固定为"根因/动机一句话"

第一段必须能独立回答"为什么非改不可"，后面的列表只是展开——
代码告诉你改了什么，提交告诉你为什么。`git log --format=full` 扫读时
只看每条第一段就能重演故障史。

### 3. 类型与 scope 白名单

- 类型（常用七类，扩展按需）：`feat` `fix` `docs` `refactor` `test`
  `ci` `chore`（标准扩展：`build` `perf`）。不造无语义新词
  （tweak/update/improve 一律不许）。
- scope = 模块/域名：`task` `dispatch` `attachment` `workspace`
  `exploration` `httpapi` `conversation` `finding` `config` `ui`
  `ci` `deploy` `docs` `sandbox`。同一主题永远同一个 scope，不漂移
  （scope 漂移是几个月后 `git log --grep` 搜不全的根源）。

## 两条红线

- **历史提交永不改写**：amend/rebase 已推送的提交会改 hash，协作者与
  镜像构建都依赖既有 hash 的稳定。
- **原子提交**：一轮工作拆成单主题提交，每条可独立回滚、可独立理解。

## 提交模板

仓库已配 `.gitmessage`（`git config commit.template` 指向它）——
`git commit` 打开编辑器时预填格式提示。模板随仓库分发，clone 后执行
`git config commit.template .gitmessage` 即可启用。
