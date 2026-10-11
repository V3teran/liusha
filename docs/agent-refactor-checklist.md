# Agent Prompt 重构验证清单

## 一、代码检查 ✅

### 1. 兜底逻辑已移除
```bash
# 验证：代码中不再有硬编码的兜底文案
grep -r "你是渗透测试" internal/ --include="*.go"
# 预期：无结果

# 验证：charter 为空时会报错
grep -A 3 "charter.*TrimSpace" internal/executor/engine.go internal/evaluator/judge.go internal/monitor/agent.go
# 预期：都有 if charter == "" { return ... error }
```

### 2. 渗透测试术语已删除
```bash
# 验证：agent prompt 不含 SQL注入/XSS/SSRF 等术语
grep -E "(SQL注入|SQLi|XSS|SSRF|RCE)" agents/*.md
# 预期：无结果或极少（通用描述除外）

# 验证：工具名硬编码已删除
grep -E "(sqlmap|nmap|nuclei|ffuf)" agents/*.md
# 预期：无结果
```

### 3. Monitor 决策表已应用
```bash
# 验证：决策表存在
grep -A 5 "| 优先级 | idle_minutes |" agents/monitor.md
# 预期：看到 4 行决策表
```

---

## 二、编译测试 ✅

```bash
# 1. 编译通过
go build ./cmd/runner
go build ./cmd/api

# 2. 单元测试通过
go test ./internal/executor/...
go test ./internal/evaluator/...
go test ./internal/monitor/...
```

---

## 三、功能测试 🔄

### 1. 启动自检（charter 为空时报错）

```bash
# 步骤 1：备份当前 agent 配置
pg_dump -t agent > /tmp/agent_backup.sql

# 步骤 2：清空 executor 的 system_prompt
psql $LIUSHA_POSTGRES_DSN -c "UPDATE agent SET system_prompt='' WHERE code='executor'"

# 步骤 3：启动 runner（预期报错）
go run ./cmd/runner
# 预期输出：
# FATAL: executor system_prompt 为空，请运行 make reseed 或在前端配置 executor agent

# 步骤 4：恢复配置
make reseed
```

### 2. 正常启动（配置完整时）

```bash
# 确保配置完整
make reseed

# 启动服务
go run ./cmd/runner &
RUNNER_PID=$!

# 验证日志（无错误）
tail -f logs/runner.log | grep -i "error\|fatal"

# 清理
kill $RUNNER_PID
```

### 3. Executor 输出通用术语

```bash
# 创建测试任务
curl -X POST http://localhost:8090/api/runs \
  -H "Content-Type: application/json" \
  -d '{"brief": "测试目标系统 http://target.local"}' \
  | jq -r '.id' > /tmp/task_id.txt

TASK_ID=$(cat /tmp/task_id.txt)

# 等待执行完成（5 分钟）
sleep 300

# 检查 observations（不应包含渗透测试术语）
curl http://localhost:8090/api/tasks/$TASK_ID/observations \
  | jq -r '.[].statement' \
  | grep -iE "(SQL注入|SQLi|XSS|SSRF|RCE|漏洞)"

# 预期：无匹配（或只有通用描述如"注入类问题"）
```

### 4. Monitor 决策表生效

```bash
# 创建长时间运行的任务
curl -X POST http://localhost:8090/api/runs \
  -H "Content-Type: application/json" \
  -d '{"brief": "长时间任务测试"}' \
  | jq -r '.id' > /tmp/long_task_id.txt

LONG_TASK_ID=$(cat /tmp/long_task_id.txt)

# 等待 Monitor 评估（6 分钟一次）
sleep 360

# 检查 Monitor 决策日志
curl http://localhost:8090/api/tasks/$LONG_TASK_ID/events \
  | jq '.[] | select(.type=="monitor_decision")' \
  | jq -r '.payload.reason'

# 预期：包含 "条件 1️⃣ 命中" 或 "条件 2️⃣ 命中" 等决策表引用
```

---

## 四、配置验证 ✅

### 1. Agent 配置完整性

```bash
# 检查 4 个 agent 的 system_prompt 非空
psql $LIUSHA_POSTGRES_DSN -c "
SELECT code, 
       LENGTH(system_prompt) as prompt_length,
       CASE 
         WHEN system_prompt = '' THEN '❌ 空'
         WHEN LENGTH(system_prompt) < 100 THEN '⚠️  太短'
         ELSE '✅ 正常'
       END as status
FROM agent 
WHERE code IN ('executor', 'evaluator', 'planner', 'monitor')
ORDER BY code;
"

# 预期输出：
# code       | prompt_length | status
# -----------|---------------|--------
# evaluator  | 10234         | ✅ 正常
# executor   | 12654         | ✅ 正常
# monitor    | 8234          | ✅ 正常
# planner    | 4534          | ✅ 正常
```

### 2. 缓存失效验证

```bash
# 修改 executor 配置
psql $LIUSHA_POSTGRES_DSN -c "
UPDATE agent 
SET system_prompt = system_prompt || E'\n\n<!-- 测试修改 -->'
WHERE code='executor'
RETURNING code, updated_at;
"

# 触发缓存失效
make reseed

# 验证：重启后读取新配置
pkill -f "cmd/runner"
go run ./cmd/runner &

# 检查日志（应该看到新配置加载）
sleep 5
curl http://localhost:8090/api/agents/executor \
  | jq -r '.system_prompt' \
  | tail -5

# 预期：包含 "<!-- 测试修改 -->"
```

---

## 五、回归测试 ✅

### 1. E2E 测试套件

```bash
# 运行完整 E2E 测试
go test -v ./cmd/e2e/... -timeout 30m

# 预期：所有测试通过
# PASS: TestFullCognitionCycle
# PASS: TestExecutorObservations
# PASS: TestEvaluatorVerification
# PASS: TestMonitorIntervention
```

### 2. 性能基准测试

```bash
# 对比修改前后的 LLM token 消耗
go test -bench=BenchmarkExecutorPrompt -benchmem ./internal/executor/...

# 预期：token 数差异 < 10%（删除示例后略有减少）
```

---

## 六、文档检查 ✅

```bash
# 验证：改进文档已创建
ls -lh docs/agent-prompt-refactor-2024.md
# 预期：文件存在，大小 > 10KB

# 验证：README 提及改进
grep -i "agent.*通用" README.md
# 预期：有相关说明（如需要可补充）
```

---

## 七、提交前检查 ✅

```bash
# 1. 查看修改统计
git diff --stat

# 预期：
# agents/evaluator.md         | 52 +++++++++++++++++++-------------------
# agents/executor.md          | 53 ++++++++++++++++++---------------------
# agents/monitor.md           | 28 ++++++++++++++-------
# agents/planner.md           | 16 ++++++------
# internal/evaluator/judge.go | 13 +++++-----
# internal/executor/engine.go | 61 ++++++++++++++++-----------------------------
# internal/monitor/agent.go   | 28 ++++++++++++++-------
# 7 files changed, 125 insertions(+), 126 deletions(-)

# 2. 检查未跟踪文件
git status

# 3. 提交
git add agents/ internal/executor/ internal/evaluator/ internal/monitor/ docs/
git commit -F /tmp/commit_message.txt

# 4. 推送
git push origin main
```

---

## 八、部署后验证 🚀

### 生产环境检查

```bash
# 1. 部署后运行 reseed
ssh production "cd /app/liusha && make reseed"

# 2. 重启服务
ssh production "systemctl restart liusha-runner liusha-api"

# 3. 健康检查
curl https://liusha.prod/api/health
# 预期：{"status": "ok"}

# 4. 验证 agent 配置
curl https://liusha.prod/api/agents/executor \
  | jq -r '.system_prompt' \
  | head -20

# 预期：包含通用描述，不含渗透测试术语

# 5. 监控日志（15 分钟）
ssh production "tail -f /var/log/liusha/runner.log" \
  | grep -iE "(error|fatal|panic)"

# 预期：无相关错误
```

---

## 九、回滚方案 ⏮️

如果生产环境出现问题：

```bash
# 1. 回滚代码
git revert HEAD
git push origin main

# 2. 重新部署
ssh production "cd /app/liusha && git pull && make reseed"
ssh production "systemctl restart liusha-runner liusha-api"

# 3. 验证恢复
curl https://liusha.prod/api/health

# 4. 保留问题现场
ssh production "journalctl -u liusha-runner --since '10 minutes ago' > /tmp/rollback-logs.txt"
scp production:/tmp/rollback-logs.txt ./logs/
```

---

## 十、成功标准 ✅

- [ ] 所有编译通过，无错误
- [ ] 单元测试全部通过
- [ ] E2E 测试全部通过
- [ ] charter 为空时启动报错
- [ ] charter 完整时正常启动
- [ ] Executor 输出不含渗透测试术语
- [ ] Monitor 使用决策表判断
- [ ] 缓存失效机制正常
- [ ] 生产环境部署成功
- [ ] 监控 15 分钟无异常

**全部通过后，此次重构视为成功完成。**
