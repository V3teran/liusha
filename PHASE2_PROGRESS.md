# Phase 2: E2E 测试重构 - 进展报告

## ✅ 已完成的工作（30%）

### 1. 核心数据模型 (cmd/e2e/models.go) ✅
- [x] `GraphStats` - 节点类型统计结构
- [x] `Path` - 推理路径验证
- [x] `AcceptanceCriteria` - 新验收标准（替代 minFindings）
- [x] `IsMetBy()` - 检查是否满足验收标准
- [x] `DiagnosticMessage()` - 诊断消息生成
- [x] `IsStuck()` - 检测任务卡住
- [x] `StuckReason()` - 返回卡住原因

### 2. HTTP 客户端 (cmd/e2e/client.go) ✅
- [x] `KnowledgeGraphClient` - 封装 HTTP 调用
- [x] `GetTaskStats()` - 调用 Phase 1 实现的 API

### 3. 轮询逻辑 (cmd/e2e/poller.go) ✅
- [x] `pollTaskWithGraphStats()` - 新轮询逻辑
  - 15 秒轮询间隔
  - 检测任务卡住
  - 检测无进展（1 分钟）
  - 详细日志输出

### 4. Profile 结构重构 (cmd/e2e/profiles.go) ✅ (部分)
- [x] 添加 `acceptance AcceptanceCriteria` 字段
- [x] 保留 `minFindings` 向后兼容
- [x] 添加 `useNewAcceptance()` 判断方法
- [x] 更新 1 个示例 profile（active:xss）

---

## 🚧 待完成的工作（70%）

### 1. 更新所有 Profiles（预计 3-4 天）

#### Passive Profiles (13 个) - 未开始
- [ ] bac - 业务访问控制
- [ ] sqli - SQL 注入
- [ ] xss - 跨站脚本（passive 版本）
- [ ] brute - 暴力破解
- [ ] lfi - 文件包含
- [ ] upload - 文件上传
- [ ] csrf - 跨站请求伪造
- [ ] api - API 安全
- [ ] cryptography - 加密问题
- [ ] redirect - 开放重定向
- [ ] authbypass - 认证绕过
- [ ] csp - 内容安全策略
- [ ] exec - 命令注入

**每个 profile 需要确定**：
```go
acceptance: AcceptanceCriteria{
    MinObjectives: ?,  // 根据任务复杂度
    MinActions:    ?,  // 根据预期动作数
    MinResults:    ?,  // 根据已知漏洞数
}
```

#### Active Profiles (5 个) - 1 个已完成
- [x] xss - XSS 专项（已更新）
- [ ] privesc - 垂直越权
- [ ] bac - 全 BAC（未授权/垂直/水平）
- [ ] full - 开放性扫描
- [ ] adhoc - 一次性扫描

### 2. Runner 集成（预计 2-3 天）

#### runActiveProfiles 重构 - 未开始
```go
// 需要修改的逻辑（行 33-142）
func runActiveProfiles(...) error {
    // 添加知识图谱客户端初始化
    kgClient := NewKnowledgeGraphClient(apiBase, apiKey)
    
    for _, ap := range profs {
        // ...
        
        // 判断使用新旧轮询
        if ap.useNewAcceptance() {
            // 新：调用 pollTaskWithGraphStats
            err := pollTaskWithGraphStats(ctx, kgClient, taskID, ap.acceptance, logger)
        } else {
            // 旧：保持现有 finding 表轮询（向后兼容）
            // 现有代码...
        }
    }
}
```

#### runPassiveProfiles 重构 - 未开始
- [ ] 类似 active，添加新旧两种轮询模式
- [ ] 保持向后兼容

### 3. 验收标准校准（预计 2-3 天）

**问题**：新标准的阈值（MinObjectives/Actions/Results）需要从实际运行中调整

**方法**：
1. 用新 API 跑一次现有的 e2e 测试
2. 记录每个 profile 的实际统计数据
3. 根据实际数据设置合理阈值

**示例**：
```bash
# 跑一个 profile，记录知识图谱统计
./scripts/dev/e2e.sh xss

# 查看统计
curl -H "X-API-Key: xxx" \
  http://localhost:8090/api/v1/tasks/{task_id}/stats

# 输出：
# {"objectives": 2, "actions": 8, "observations": 8, "evaluations": 5, "results": 3}

# 设置阈值（留 20% 余量）
MinObjectives: 1  # 2 * 0.8 = 1.6 → 1
MinActions:    6  # 8 * 0.8 = 6.4 → 6
MinResults:    2  # 3 * 0.8 = 2.4 → 2
```

### 4. 测试和调试（预计 2 天）
- [ ] 编译测试
- [ ] 单个 profile 测试
- [ ] 全量 e2e 测试
- [ ] 修复发现的问题

---

## 📋 实施建议

### 方案 A：渐进式迁移（推荐）
**时间**：1-2 周，分阶段交付

1. **Week 1, Day 1-2**：完成 runner 集成
   - 实现新旧轮询切换逻辑
   - 保持向后兼容

2. **Week 1, Day 3-5**：更新 5 个 active profiles
   - 从简单的开始（xss 已完成）
   - 逐个测试验证

3. **Week 2, Day 1-3**：更新 13 个 passive profiles
   - 批量更新
   - 集中测试

4. **Week 2, Day 4-5**：校准阈值 + 最终验收
   - 运行全量测试
   - 调整不合理的阈值

**优势**：
- 每个阶段都可以独立验收
- 风险可控，可以随时回退到旧标准
- 团队可以提前看到效果

### 方案 B：一次性完成
**时间**：3-4 天集中投入

1. **Day 1**：完成 runner + 5 个 active profiles
2. **Day 2**：完成 13 个 passive profiles
3. **Day 3**：校准阈值 + 测试
4. **Day 4**：修复问题 + 验收

**优势**：
- 完成时间更短
- 不需要维护两套标准的并存

**劣势**：
- 风险集中
- 如果遇到问题回退成本高

---

## 🎯 当前状态

```
Phase 2 进度：30% ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100%
              ████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░

已完成：
✅ models.go    - 核心数据模型
✅ client.go    - HTTP 客户端
✅ poller.go    - 新轮询逻辑
✅ profiles.go  - 结构重构 + 1 个示例

待完成：
⬜ runner.go    - 集成新轮询
⬜ profiles.go  - 更新剩余 17 个 profiles
⬜ 阈值校准     - 从实际运行中调整
⬜ 测试验收     - 全量 e2e 测试
```

---

## 📝 下一步行动

### 立即可做（建议）
1. **完成 runner.go 集成**
   - 实现新旧轮询切换
   - 保持向后兼容
   - 时间：半天

2. **更新 active profiles**
   - 只有 5 个，相对简单
   - 可以快速看到效果
   - 时间：1 天

3. **运行一次测试，校准阈值**
   - 用实际数据调整 MinObjectives/Actions/Results
   - 时间：半天

### 中期目标（本周内）
- 完成所有 active profiles 迁移
- 验证新轮询逻辑稳定性
- 生成校准数据供 passive profiles 参考

### 长期目标（下周）
- 完成所有 passive profiles 迁移
- 全量 e2e 测试通过
- 更新文档和 README

---

## 💡 技术亮点

### 1. 向后兼容设计
```go
// 同时支持新旧两种标准
type activeProfile struct {
    acceptance  AcceptanceCriteria  // 新标准（优先）
    minFindings int                 // 旧标准（兜底）
}

// 自动选择
if ap.useNewAcceptance() {
    // 使用知识图谱 API
} else {
    // 使用 finding 表
}
```

### 2. 智能诊断
```go
// 不仅判断通过/失败，还能诊断原因
if IsStuck(stats) {
    return fmt.Errorf("任务卡住: %s", StuckReason(stats))
}

// 输出诊断消息
fmt.Println(criteria.DiagnosticMessage(stats))
// "目标不足: 0/1; 动作不足: 2/5; 结果不足: 0/3;"
```

### 3. 渐进式验证
```go
// 可以先只验证节点数量
acceptance: AcceptanceCriteria{
    MinObjectives: 1,
    MinActions:    5,
    MinResults:    3,
    // MustHavePaths: []Path{...}  // Phase 2.2 再实现
}
```

---

## 📚 参考资料

- [Phase 1 完成报告](PHASE1_COMPLETE.md)
- [完整迁移计划](docs/E2E_MIGRATION_PLAN_UPDATED.md)
- [知识图谱 API 文档](internal/httpapi/knowledge_graph_handler.go)
