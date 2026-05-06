# Liusha e2e 跑法

端到端验收触发器 `cmd/e2e`：建 engagement → 一次预录全 profile 凭证 → 发样本流量 →
轮询 finding 直到达标。每个 profile 独立 engagement、串行跑。

## 1. 准备

确认基础设施已起：

```bash
make up        # postgres + redis（docker）
make migrate
```

`.env.local` 至少需要：

```bash
DEEPSEEK_API_KEY=sk-...
LIUSHA_API_KEY=changeme-dev-key   # 与 run-svc.sh 默认一致
```

## 2. 起 host 服务

`run-svc.sh` 并行起 4 个 host 进程（vulnapp:8001 / proxy:8888+9091 / api:8090 / scanner:9090），日志写 `logs/{vulnapp,proxy,api,scanner}.log`：

```bash
./scripts/dev/run-svc.sh    # 占用一个终端
```

## 3. 跑触发器

**最常用三种用法**（另开终端）：

```bash
make e2e             # 不带参 = 跑全部 profile（bac + sqli）
make e2e-bac         # 仅 bac
make e2e-sqli        # 仅 sqli
```

**多选 / 自定义参数**（直接调 binary 或脚本）：

```bash
go run ./cmd/e2e bac sqli              # 多选，串行跑
./scripts/dev/e2e.sh bac sqli          # 同上 + 前置健康检查
./scripts/dev/e2e.sh                   # 无参 = 全部
```

**关键行为**：

- **启动期一次预录所有 profile 全部 host 的凭证**——不论本次跑哪些 profile，凭证池都会被填好（localhost 三身份 + 49.234.23.42 admin）。
- 每个 profile 独立建 engagement、独立轮询 finding。
- 任一 profile 失败 → 退出码 = 失败 profile 数；全部成功 → 退出码 0。

## 4. 内置 profile

| 名 | 样本 | scope_host | 身份 | 验收门槛 |
|----|------|-----------|------|---------|
| bac | examples/sample_bac_raw.json | localhost | admin / test / m233241 | ≥3 finding，3 类齐全（unauthorized + vertical + horizontal）|
| sqli | examples/sample_sqli_raw.json | 49.234.23.42 (DVWA) | admin (PHPSESSID) | ≥1 finding（如 sqli.error_based）|

## 5. 加新漏洞类型

1. `cmd/e2e/main.go:profiles` map 加一行（`name / defaultSamples / kindPrefix / minFindings / minKinds / credsForHost`）
2. `examples/sample_<vuln>_raw.json` 写样本流量（一组 raw HTTP/1.1 字符串数组）
3. 可选：`Makefile` 加 `e2e-<vuln>` 别名

## 6. 检查结果

```bash
# 漏洞汇总
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT kind, severity, confidence, title
  FROM vuln_finding
  ORDER BY created_at DESC;"

# LLM 成本
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT route_key, COUNT(*) AS calls, ROUND(SUM(cost_usd)::numeric, 6) AS cost
  FROM llm_invocation
  GROUP BY route_key
  ORDER BY cost DESC;"

# host_lesson（finding 蒸馏出的跨 engagement 经验）
docker exec liusha-postgres psql -U liusha -d liusha -c "
  SELECT host, content
  FROM host_lesson
  ORDER BY priority DESC, hit_count DESC;"
```

## 7. 排查

### `e2e timeout: 未达 finding/类覆盖门槛`
- `tail -F logs/scanner.log` 看 ReAct 循环
- DB 看 http_flow 是否有数据：`SELECT count(*) FROM http_flow;`——为 0 表示流量未通过 proxy 落库
- Redis stream 是否在消费：`docker exec liusha-redis redis-cli XLEN liusha:flow_events`

### `connection refused`
- `curl -sf http://localhost:8090/healthz` 检查 api
- 确认 `LIUSHA_API_KEY` 与 `.env.local` 一致

### LLM 调用 4xx/5xx
- DEEPSEEK_API_KEY 是否有效
- `logs/scanner.log` 里 grep `error.*deepseek`

### Redis FLUSHDB 后 ingestor NOGROUP
- 重建 consumer group：`docker exec liusha-redis redis-cli XGROUP CREATE liusha:flow_events liusha-ingestor 0`
- 或重启 scanner 让其在启动期重建

## 8. 关停

```bash
# 在 run-svc.sh 终端按 Ctrl-C；或手动：
pkill -f 'cmd/(api|scanner|proxy|vulnapp)'

# 关基础设施
make down
```
