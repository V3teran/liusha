# Liusha v1 e2e 跑法

> Plan 2 T10 完整 e2e 流程。前置阶段（核心栈起 + migrate + schema 验证）已自动跑通，本文档指引剩余 4 步——真正消耗 LLM API key 的部分留给你手动跑。

## 前置确认（已完成 ✅）

- docker 容器：`liusha-postgres`（pgvector pg17 healthy）+ `liusha-redis`（redis:8 healthy）
- 镜像 build：`liusha/api:latest` + `liusha/scanner:latest` 已存在
- 数据库 schema：8 业务表 + `schema_migrations` 全部 migrate 完毕
- 三层 memory（`memory_facts/ideas/hints`）+ `llm_call.role` 字段全部正确建立

如果 docker 容器不在了：

```bash
make up   # 起 postgres + redis
make migrate
```

## 完整 e2e 跑法（消耗 LLM API key）

### 1. 准备 .env.local

```bash
cp .env.example .env.local
# 编辑 .env.local，填入 DEEPSEEK_API_KEY 和 ANTHROPIC_API_KEY
# DEEPSEEK_API_KEY 必填（默认主 LLM）
# ANTHROPIC_API_KEY 可选（用于 light_provider 跑 Observer/Distill）
# OPENAI_API_KEY 可选（fallback_provider）
```

### 2. 起完整栈（含 vulnapp + proxy + scanner）

```bash
docker compose -f deployments/docker-compose.yml --profile e2e --env-file .env.local up -d --build
```

约 30-60 秒，等所有 service healthy：

```bash
docker compose -f deployments/docker-compose.yml ps
```

期望看到 6 个 service（postgres/redis/api/proxy/scanner/vulnapp）全绿。
其中 `proxy` 由 `cmd/proxy` 内嵌 proxify SDK + filter/dedup/aggregator 启动（监听 :8888 mitm，:9091 healthz），
`scanner` 仅作为 Asynq 消费者 + ReAct 引擎（监听 :9090 healthz）。两者通过 redis 解耦——业界最佳实践，故障隔离 + 独立扩缩。

### 3. 跑触发器（约 6 分钟）

```bash
make e2e-bac
```

或直接：

```bash
DEEPSEEK_API_KEY=$DEEPSEEK_API_KEY \
LIUSHA_POSTGRES_DSN=postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable \
go run ./cmd/e2e-bac
```

预期日志：

```
engagement_id=...
✓ credentials enrolled
✓ 18 requests sent through proxy
findings: 0 (BAC: 0)
findings: 1 (BAC: 1)   ← scanner sniffer + BAC subtask 跑起来了
findings: 3 (BAC: 3)
findings: 5 (BAC: 5)   ← 退出条件
✓ 5 findings + 3 类齐全
✓ 黑客松借鉴 4 项断言全部通过
```

退出 code：
- `0` 成功
- `1` timeout（6min 内未拿到 5 finding）
- `2` 黑客松借鉴断言失败

### 4. 检查结果

```bash
# 查 findings
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT kind, severity, title, dedup_key
  FROM finding
  WHERE engagement_id IN (SELECT id FROM engagement WHERE scope_host='vulnapp')
  ORDER BY created_at;"

# 查 LLM 成本
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT role, COUNT(*) AS calls, ROUND(SUM(cost_usd)::numeric, 6) AS cost
  FROM llm_call
  WHERE engagement_id IN (SELECT id FROM engagement WHERE scope_host='vulnapp')
  GROUP BY role
  ORDER BY cost DESC;"

# 查 memory 三层
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT
    jsonb_array_length(COALESCE(memory_facts->'evidence', '[]'::jsonb)) AS facts_evidence,
    jsonb_array_length(COALESCE(memory_facts->'boundaries', '[]'::jsonb)) AS facts_boundaries,
    jsonb_array_length(COALESCE(memory_ideas->'hypotheses', '[]'::jsonb)) AS ideas,
    jsonb_array_length(COALESCE(memory_hints->'hints', '[]'::jsonb)) AS hints
  FROM engagement WHERE scope_host='vulnapp';"
```

### 5. 关闭栈

```bash
docker compose -f deployments/docker-compose.yml --profile e2e down -v
```

`-v` 删掉 volume（pg/redis 数据 + proxify JSONL）；下次跑要重新 `make migrate`。

## 期望结果（成功标志）

| 验收项 | 期望 | 来源 |
|---|---|---|
| BAC findings | ≥ 5 条 | spec §10.2 + plan 2 T9 |
| 漏洞类型齐全 | unauthorized / vertical / horizontal 三类 | spec §10.2 |
| `memory_facts` 非空 | evidence 数组 ≥ 1 条 + boundaries 数组 ≥ 1 条 | 黑客松借鉴 B（三层 memory） |
| `memory_ideas` 非空 | hypotheses 数组 ≥ 1 条 | 黑客松借鉴 B |
| `memory_hints` 非空 | distill hint 至少 1 条（from_skill='vuln/web/bac'） | 黑客松借鉴 F8（经验自蒸馏） |
| `llm_call.role` 多元 | observer 或 distill 至少 1 次（光走 react.main 不算） | 黑客松借鉴 F11（多模型路由） |
| 异常终止 | observer_abort + done_force 数 = 0 | 黑客松借鉴 A/C 不该误触发 |
| 总成本 | < $0.50（v1 预算） | spec §11 |

## 排查

### 触发器报"connection refused"
- 确认 `liusha-api` 容器健康：`docker compose ps`
- 确认 `LIUSHA_API_KEY` 与 .env.local 一致

### findings 卡在 0 不增长
- 看 `scanner` 日志：`docker compose logs -f scanner`
- 看 `proxy` 日志确认流量经过：`docker compose logs proxy | grep vulnapp`
- 看 proxy 进程 aggregator flush + sniffer enqueue：
  ```
  grep -i "窗口关闭\|sniffer 入队\|aggregator" logs/proxy.log
  ```

### LLM 调用失败
- DEEPSEEK_API_KEY 是否有效：`curl -H "Authorization: Bearer $DEEPSEEK_API_KEY" https://api.deepseek.com/chat/completions ...`
- 看 `scanner` 日志中的 4xx/5xx 错误码
- T21.5 RetryDecorator 应自动重试 + fallback；如果 fallback_provider 也挂，整个 task 才会 error

### 黑客松断言失败
- `verify_borrowed.go` 输出哪条不通过：
  - `memory 三层有空` → BAC SKILL.md 引导可能太弱，sniffer 没调 write_fact/idea
  - `无 BAC distill hint` → finding.OnSaved hook 未触发（看 distill log）
  - `无 observer/distill llm_call` → Router 路由没生效（cfg.LLM.Routes 缺）
  - `出现 observer_abort/done_force` → BAC SKILL.md 步骤导致 LLM 死循环或被 LoopDetector 拦下

## v1 范围之外（v1.5 / v2 才做）

- SQLi 检测（vuln/web/sqli skill）
- browser 模式（operator 角色 + browser.* actions）
- 多 sniffer 并发 + sibling 协作（黑客松全局 idea 共享）
- 多模型路由动态调度（按当前成本预算）
