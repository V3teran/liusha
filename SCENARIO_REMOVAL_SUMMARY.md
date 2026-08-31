# Scenario 概念完全删除总结

## 执行时间
2026-08-31

## 删除原因
scenario 概念已废弃，不再需要场景配置，前后端都无需此概念。

---

## 已完成的修改

### 1. 数据库层
- ✅ 创建迁移 `0127_remove_scenario_concept.up.sql`
  - DROP TABLE scenario CASCADE
  - ALTER TABLE task DROP COLUMN scenario_id
  - ALTER TABLE assignment DROP COLUMN scenario_id
  - ALTER TABLE cron_schedule DROP COLUMN scenario_id
  - DROP INDEX task_scenario_status_idx

### 2. 代码层 - 包/目录删除
- ✅ 删除 `internal/config/scenario/` 整个包
- ✅ 删除 `scenarios/` 目录（web-pentest.md、api-pentest.md）

### 3. 核心模型修改
- ✅ `internal/task/model.go`
  - 删除 `Task.ScenarioID` 字段
  - 删除 `NewParams.ScenarioID` 字段
  - 更新包注释

- ✅ `internal/task/store.go`
  - 修改 `colsSelect` 常量（移除 scenario_id）
  - 修改 `Create()` 函数（移除 scenario_id 参数）
  - 修改 `List()` 函数（移除 scenarioID 过滤）
  - 修改 `scan()` 函数

- ✅ `internal/assignment/model.go`
  - 删除 `Assignment.ScenarioID` 字段
  - 删除 `NewParams.ScenarioID` 字段
  - 更新包注释

- ✅ `internal/assignment/store.go`
  - 修改 `colsSelect` 常量
  - 修改 `Create()` 函数
  - 修改 `scan()` 函数

- ✅ `internal/conversation/model.go`
  - 删除 `Conversation.ScenarioID` 派生字段

- ✅ `internal/conversation/store.go`
  - 修改 `ListConversations()` 函数签名（移除 scenarioID 参数）
  - 修改 SQL 查询（移除 scenario_id JOIN 和过滤）
  - 修改 `scanConversationWithRun()` 函数

### 4. HTTP API 层
- ✅ `internal/httpapi/server.go`
  - 删除 `/scenarios` 系列路由（GET/POST/PUT/DELETE）
  - 删除 `/findings/scenarios` 路由
  - 更新 `Deps.ConfigStore` 注释

- ✅ `internal/httpapi/config_handler.go`
  - 删除 `cfgscenario` import
  - 删除 `ConfigAPI` 接口中的所有 scenario 方法
  - 删除 `listScenariosHandler()`
  - 删除 `getScenarioHandler()`
  - 删除 `saveScenarioHandler()`
  - 删除 `deleteScenarioHandler()`
  - 删除 `scenarioBody` 类型
  - 删除 `scenarioJSON()` 函数
  - 更新注释

---

## 待处理项（需要继续完成）

### 高优先级
- ⏳ `internal/finding/store.go` - 删除 `Scenarios()` 方法
- ⏳ `internal/cronschedule/` - 删除 scenario_id 字段
- ⏳ `internal/configstore/` - 删除所有 scenario 相关方法
- ⏳ `internal/httpapi/handlers.go` - 删除 `findingScenariosHandler()`
- ⏳ `internal/httpapi/finding_handler.go` - 删除相关函数
- ⏳ `cmd/api/main.go` - 清理 scenario 引用
- ⏳ `cmd/runner/main.go` - 清理 scenario 引用

### 中优先级
- ⏳ 所有 `*_test.go` 文件 - 更新测试用例
- ⏳ `internal/ingestor/traffic.go` - 清理引用
- ⏳ `internal/worker/handler.go` - 清理引用
- ⏳ `internal/domain/profile.go` - 清理引用

### 低优先级
- ⏳ 文档更新（docs/）
- ⏳ 配置文件示例

---

## 批量清理脚本

已创建 `scripts/remove_scenario_bulk.sh` 用于批量处理剩余引用。

## 验证步骤

清理完成后需要执行：

```bash
# 1. 编译检查
go build ./...

# 2. 运行测试
go test ./...

# 3. 数据库迁移
migrate -path db/migrations -database "$LIUSHA_POSTGRES_DSN" up

# 4. 启动服务验证
./api
./runner
./proxy
```

## 影响评估

- **数据库**：DROP scenario 表，删除 3 个外键列
- **API 接口**：删除 5+ 个 HTTP 端点
- **前端**：需要删除场景选择器相关组件
- **配置**：scenario 配置文件已无效

## 注意事项

1. ⚠️ **数据迁移不可逆**：旧的 scenario 配置将永久丢失
2. ⚠️ **历史数据**：已有 task 的 scenario_id 将被删除
3. ⚠️ **前端适配**：前端代码需要同步删除场景相关功能
4. ⚠️ **API 兼容性**：调用 `/scenarios` 的客户端将收到 404

## 预估工作量

- 已完成：约 40%
- 剩余工作量：约 85 个文件，400+ 处引用
- 预计耗时：2-3 小时（手动修复编译错误 + 测试）
