# Scenario 删除 - 当前状态和建议

## 当前进度：约 45%

### ✅ 已完成
- 数据库迁移创建
- 核心模型修改（task/assignment/conversation）
- HTTP API 路由删除
- 批量文本清理（274处→更少）

### ❌ 主要编译错误（需手动修复）

#### 1. internal/configstore/store.go
**问题**：整个 scenario 子系统仍存在
**需要删除**：
- `scenarioStore` 接口（29-39行）
- `Store.scenarios` 字段（57行）
- `New()` 中的 `cfgscenario.NewStore(pool)` 调用（66行）
- 所有 `Scenario*()` 方法（约100行）
- scenario 相关缓存键函数

#### 2. internal/config/seed/seed.go
**问题**：scenario 导入逻辑仍存在
**需要删除**：
- `importScenarios()` 函数
- 所有 cfgscenario 引用

#### 3. internal/finding/store.go
**需要删除**：`Scenarios()` 聚合方法

#### 4. internal/cronschedule/
**需要删除**：scenario_id 字段相关代码

#### 5. cmd/e2e/runner.go
**需要修复**：`ts.List()` 调用参数

#### 6. internal/ingestor/traffic.go
**需要删除**：`ScenarioID` 字段赋值

---

## 🎯 建议行动方案

**方案1：我继续手动修复（预计1-2小时）**
- 优点：精确控制，理解每处修改
- 缺点：耗时长，容易遗漏

**方案2：暂停，提交当前进度，标记为 WIP**
```bash
git add .
git commit -m "WIP: scenario 删除进度 45%

已完成：
- 数据库层删除
- 核心模型清理
- HTTP API 删除

待修复编译错误：
- configstore 完整重构
- seed 导入清理
- finding/cronschedule 更新

详见 SCENARIO_REMOVAL_SUMMARY.md"
```

**方案3：我提供完整的重写文件（configstore.go/seed.go等）**
- 直接重写关键文件，删除所有 scenario 代码
- 快速但风险较高

---

## 📊 工作量评估

- **手动修复剩余错误**：1-2 小时
- **修复所有测试**：1 小时
- **端到端验证**：30 分钟
- **总计**：2.5-3.5 小时

---

## 你的选择？

A. 继续手动修复（我逐个文件处理）
B. 提交当前进度（标记 WIP）
C. 我重写关键文件（快速但需要你验证）
D. 其他建议
