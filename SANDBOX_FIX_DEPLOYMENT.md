# 🔧 Sandbox 修复部署指南

## 问题总结
当前运行的 sandbox-server 是旧版本，使用 `hunter_id` 字段，而新架构已改为 `executor_id`。

---

## ✅ 代码已修复

### 修改内容
```go
// internal/sandbox/types.go
type ExecRequest struct {
    ExecutorID     string `json:"executor_id"`  // ✅ 已修复
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

### 提交记录
- `ab6750e7`: fix: 统一 Sandbox ExecRequest JSON 字段名为 executor_id

---

## 📋 部署步骤

### 1. 重新构建 sandbox-server 镜像

```bash
# 构建新镜像
docker build -t liusha-sandbox-server:latest -f Dockerfile.sandbox-server .

# 或者如果有 Makefile
make build-sandbox-server
```

### 2. 停止所有旧的 sandbox 容器

```bash
# 查看当前运行的 sandbox 容器
docker ps | grep sandbox

# 停止并删除所有 sandbox 容器
docker stop $(docker ps -q --filter "name=sandbox") 2>/dev/null
docker rm $(docker ps -aq --filter "name=sandbox") 2>/dev/null
```

### 3. 重启 runner（自动使用新镜像）

```bash
# 停止旧 runner
pkill -f "liusha-runner"

# 启动新 runner
XIAOMI_API_KEY=<your-key> \
LIUSHA_POSTGRES_DSN=postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable \
LIUSHA_REDIS_ADDR=localhost:6379 \
LIUSHA_ENV=development \
LIUSHA_LOG_LEVEL=info \
LIUSHA_LLM_KEY_SECRET=<your-secret> \
LIUSHA_LLM_FALLBACK=mimo \
./liusha-runner &
```

### 4. 验证修复

```bash
# 提交测试任务
TASK_ID=$(curl -s -X POST http://localhost:8090/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: changeme-dev-key" \
  -d '{
    "brief": "测试 http://111.229.193.40:34280/login.php，账号 admin/password",
    "message": "验证 sandbox 修复"
  }' | jq -r '.task_id')

echo "Task ID: $TASK_ID"

# 等待 60 秒
sleep 60

# 检查 run_command 是否成功
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT 
  tool_name,
  COUNT(*) as total,
  COUNT(*) FILTER (WHERE error_message IS NULL OR error_message = '') as success
FROM tool_invocation 
WHERE task_id = '$TASK_ID' AND tool_name = 'run_command'
GROUP BY tool_name;
"

# 检查是否产生了 findings
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT COUNT(*) as finding_count 
FROM finding 
WHERE task_id = '$TASK_ID';
"
```

---

## 🎯 预期结果

### 修复前
```
run_command: 沙箱执行失败: sandbox /exec: status 400: 
  hunter_id required (subtask swarm 按 hunter 切目录隔离)
```

### 修复后
- ✅ `run_command` 执行成功
- ✅ 产生 findings
- ✅ 完整的渗透测试流程畅通

---

## 🐛 故障排查

### 如果仍然报 `hunter_id required`
说明旧容器还在运行，执行：
```bash
docker ps -a | grep sandbox
docker rm -f <container-id>
```

### 如果报 `executor_id required`
这是正常的新错误消息，说明代码已更新，但可能参数传递有问题。
检查工具调用日志。

### 如果 run_command 仍然失败但错误不同
检查 tool_invocation 表的 error_message：
```sql
SELECT error_message, COUNT(*) 
FROM tool_invocation 
WHERE tool_name = 'run_command' AND error_message IS NOT NULL
GROUP BY error_message;
```

---

## 📊 成功指标

部署成功后，应该看到：

1. ✅ `run_command` 成功率 > 80%
2. ✅ Findings 产出率 > 0%
3. ✅ Actions 完成率 > 80%
4. ✅ 完整的 E2E 流程畅通

---

## 🎉 总结

### 架构理解
- **Assignment**: Sandbox 容器的生命周期
- **ExecutorID**: Agent 的 ID，用于工作目录隔离
- **hunter**: 旧架构概念，已废弃 ❌

### 修复内容
- ✅ JSON 字段名统一为 `executor_id`
- ✅ 与 Go 字段名一致
- ✅ 与 server 验证逻辑一致
- ✅ 代码层面完全修复

### 待完成
- 🔄 重新部署 sandbox-server 容器
- 🔄 验证修复效果
