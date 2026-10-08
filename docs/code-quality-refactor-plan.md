# Liusha 代码质量治理计划

## 第1轮：基础修复 ✅ (已完成)

### 已修复问题
1. **代码格式问题** - 9个文件的 gofmt 格式问题
2. **测试错误处理** - 4处未检查的错误返回值  
3. **原子操作** - CompletionDetector 的类型断言增强
4. **测试依赖** - monitor 测试补充缺失参数

### 提交记录
- Commit: `cea94eba` - "fix: 修复代码格式和测试问题"

---

## 第2轮：大函数拆分 (进行中)

### 待重构的大函数 (>100行)

#### 1. internal/executor/engine.go:Execute (222行) - 优先级：高
**问题**：单一函数承担了太多职责
- 参数解析和验证
- LLM 上下文构建
- 工具注册
- ReAct 执行配置
- 结果解析
- Observation 节点创建

**建议拆分**：
```go
// 主流程函数（保持 ~50 行）
func (e *Engine) Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, error)

// 提取的辅助函数：
func (e *Engine) parseActionContent(action explorationgraph.Node) (*ActionData, error)
func (e *Engine) prepareReActTools() []registry.Tool
func (e *Engine) buildReActConfig(ctx context.Context, action ActionData, tools []registry.Tool) (*runtime.ReActConfig, error)
func (e *Engine) executeReAct(ctx context.Context, runtime *runtime.ReActRuntime, config *runtime.ReActConfig) (*runtime.ReActResult, error)
func (e *Engine) processExecutorOutput(ctx context.Context, action explorationgraph.Node, result *runtime.ReActResult) ([]evaluator.Attempt, error)
```

#### 2. internal/planner/intelligence.go:buildPlanningPrompt (179行) - 优先级：中
**问题**：长 prompt 构建逻辑混杂业务规则

**建议**：
- 拆分为多个 prompt 段构建函数
- 使用模板文件或常量

#### 3. internal/httpapi/server.go:registerRouteGroups (148行) - 优先级：中
**问题**：路由注册过于集中

**建议**：
- 按业务模块拆分路由组（tasks, assignments, findings 等）
- 每个模块一个独立的路由注册函数

#### 4. internal/evaluator/verifier.go:Promote (148行) - 优先级：中
**问题**：验证和晋升逻辑混合

**建议拆分**：
```go
func (v *PromotionEvaluator) Promote(ctx context.Context, a Attempt) (*explorationgraph.Node, error)
func (v *PromotionEvaluator) validateAttempt(a Attempt) error
func (v *PromotionEvaluator) buildVerificationInput(a Attempt) (*VerificationInput, error)
func (v *PromotionEvaluator) createResultNode(ctx context.Context, a Attempt, verification *VerificationResult) (*explorationgraph.Node, error)
```

---

## 第3轮：大文件拆分 (待代理完成)

### 待拆分的大文件 (>500行)

1. **internal/framework/core/graphstore_postgres.go** (822行)
   - 建议：按操作类型拆分（CRUD、查询、更新等）
   
2. **internal/config/config.go** (712行)
   - 建议：按配置域拆分（API、Runner、LLM、Sandbox 等）

3. **internal/planner/agent.go** (603行)
   - 建议：拆分事件处理、规划逻辑、结果分析

4. **internal/explorationgraph/adapter.go** (573行)
   - 建议：按节点类型拆分操作

---

## 第4轮：代码质量改进 (规划中)

### 错误处理规范
- ✅ 大部分错误使用 `fmt.Errorf` 包装
- ✅ 错误链保持完整
- ⚠️ 部分地方可以添加更多上下文信息

### 日志规范
- ✅ 使用 zerolog 统一日志
- ✅ 日志级别使用合理
  - Info: 87次
  - Warn: 46次  
  - Error: 43次
  - Debug: 25次

### 资源管理
- ✅ 仅13处 goroutine 启动
- ✅ 大部分有适当的 context 和 WaitGroup
- ⚠️ 需要审查是否都有超时控制

---

## 第5轮：性能优化 (待执行)

### 潜在优化点
1. 减少数据库查询
2. 添加更多缓存层
3. 批量操作优化
4. 并发控制优化

---

## 第6轮：测试覆盖 (待执行)

### 当前状态
- 所有单元测试通过 ✅
- 无竞态条件 ✅
- 测试覆盖率待评估

### 改进方向
1. 增加边界测试
2. 增加错误路径测试
3. 增加集成测试
4. 性能基准测试

---

## 第7-10轮：专项改进

待第1-6轮完成后，根据实际情况制定后续计划：
- 文档完善
- API 设计优化
- 监控和可观测性
- 安全加固

---

## 执行优先级

### P0 (立即执行)
- ✅ 基础格式和测试修复

### P1 (本周完成)
- [ ] Execute 函数拆分
- [ ] 其他大函数初步拆分

### P2 (下周完成)  
- [ ] 大文件拆分方案确定
- [ ] 开始执行文件拆分

### P3 (后续迭代)
- [ ] 性能优化
- [ ] 测试覆盖提升
- [ ] 文档完善
