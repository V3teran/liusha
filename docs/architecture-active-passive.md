# active / passive 两条路线架构

> 本文基于源码调用链梳理（非注释），日期 2026-07-12。
> 关键文件：`cmd/api/main.go`(下发)、`internal/ingestor/traffic.go`(聚合器)、
> `cmd/scanner/handler_active_eino.go` / `handler_passive_eino.go`(执行)、
> `internal/einoagent/`(agent 装配)、`internal/lead` / `internal/lesson`(共享轴)。

## 0. 一句话大局

两轨在 **task 及以下完全同构**，只在 **task 之上分叉**：

- **active**：人提一个 brief → `assignment` fan-out 成 N 个 task（拆网站，散）。
- **passive**：代理自动捕获流量 → 聚合器 fan-in 成 1 个 task（攒批，聚）。

方向相反，产出的 task 同构，下游走同一套（1 task = 1 对话 = N 次 hunter run = 挂 task 的 finding/图 + 按 host 的 lead/lesson/credential）。

## 1. ACTIVE 全链路

```
① 前端下发：POST /scan/active  或  对话发起 StartChatScan
        │
        ▼
② cmd/api createScan()                         【一切下发皆走 assignment §3.1】
   ├─ assignments.Create(active, manual, [brief])   建"下发单"
   └─ expandActiveItem():
        ├─ tasks.Create(active, assignment_id)      建"这次扫描"(status=active)
        ├─ hunters.Create(role=orchestrator)        建第一个 agent run
        └─ enqueue → asynq   (MaxRetry(0)：跑挂不重试，人工重发)
        │
        ▼  ~~ 跨进程，消息进 asynq/Redis 队列 ~~
③ cmd/scanner handle()
   ├─ per-host 限速闸(§4.3)：同 host 并发>2 → 退避重排队   防打爆/被封 IP
   └─ switch task.mode → handleActiveEino()
        │
        ▼
④ handleActiveEino()  组装 eino deep swarm(一主多子)
   ├─ Heartbeat(task)          心跳起点挪到"真接手"，防排队被 reaper 冤杀
   ├─ 从 brief 抽 host 回填 task.target_host
   ├─ 起 1 个 sandbox(主+子共享)
   ├─ watchAbortActive()：轮询 task.status，被 abort 就 cancel
   └─ RunDeepSwarm:
      ┌────────────────────────────────────────┐
      │ orchestrator(主, hunters/active/orchestrator.md)         │
      │   用 deep 内建 task 工具按攻击面派活(子代理串行)          │
      │   ├──► reconnaissance(侦察子代理)                        │
      │   └──► exploitation (利用子代理)  内部多工具并行(ToolsNode)│
      │   工具：run_command/browser/list_flows/view_flow/        │
      │        replay_flow/write_finding/write_lead/write_lesson │
      └────────────────────────────────────────┘
        │
        └─ agent 在 sandbox 打的请求 → POST /internal/v1/flows/ingest
             → handleInternalSnap：反查 hunter→task_id
             → 落 agent_traffic(挂 task) 【不 enqueue，防自激震荡】
             (list/view/replay_flow 就读这张表)
        ▼
⑤ finalizeScan()  收尾(成功/失败/中止都走)
   ├─ 成功 → tasks.Complete(task)   置终态 completed
   ├─ 失败/中止 → tasks.Abort(task, reason)
   └─ leads.ExpireHost(host, 7天)   该 host 情报设冷却 TTL
```

**active 的流量是"副产品"**：agent 自己在 sandbox 打的请求落 `agent_traffic`，挂 task，只当 replay/查看弹药，绝不触发分析（否则死循环）。

## 2. PASSIVE 全链路

```
① 用户浏览器正常上网 → 流量经 sanitizer 代理 → flow_events(Redis Stream)
        │
        ▼  ~~ 跨进程 ~~
② cmd/scanner ingestor.Run()  XReadGroup
   ├─ 消费组 liusha-ingestor            多副本共享(负载均衡)
   └─ 消费者名 ingestor-<host>-<pid>    每实例唯一
        │
        ▼
③ handleMessage → 按 source 分流 → handleExternalSnap()   (代理捕获=external)
   ├─ 落 proxy_traffic(挂 host, consumed_by_task_id=NULL)  流量先落库，不需先有 task
   └─ agg.observe(host)：Redis 窗口 INCR + 首条时间戳
        │  攒够 20 条 / 距首条 10s(先到先触发)
        ▼
④ claimAndReset(host)  Redis 抢锁(SET NX)   多副本只一个抢到，幂等
        │ 抢到
        ▼
⑤ spawnPassiveTask(host)                       【同样走 assignment】
   ├─ assignments.Create(passive, auto, [host])
   ├─ tasks.Create(passive, target_host=host)
   ├─ proxyFlows.ClaimUnconsumedByHost(task,host)  回填 consumed_by_task_id
   │     条件更新 WHERE consumed_by IS NULL；领到 0 条 → Abort 空 task(幂等 §13.2)
   ├─ ensureConversation(task)                     绑对话(与 active 对称)
   └─ enqueuePassive → asynq
        │
        ▼  ~~ 跨进程 ~~
⑥ handle() → per-host 闸 → switch mode → handlePassiveEino()
        │
        ▼
⑦ handlePassiveEino()  单个 agent(无 swarm)
   ├─ Heartbeat(task) / 起 sandbox / watchAbort()
   └─ RunTrafficAnalysis:
      ┌────────────────────────────────────────┐
      │ traffic-analysis(单 ChatModelAgent, hunters/passive/*.md)│
      │   list_flows/view_flow/replay_flow ← 读 proxy_traffic     │
      │     (按 consumed_by_task_id=本 task 限定这批)             │
      │   write_finding/write_lead/write_lesson                   │
      └────────────────────────────────────────┘
        ▼
⑧ finalizeAnalysis()  收尾
   ├─ 成功 → tasks.Complete(task)   (修复前 passive 从不置终态，靠 reaper 误判 aborted)
   └─ 失败/中止 → tasks.Abort(task, reason)
```

**passive 的流量是"分析对象"**：代理抓的真实用户流量落 `proxy_traffic`，挂 host（先于任何 task），攒批后回填"被哪个 task 吃了"。

## 3. 共享轴（两轨共写共读）

```
              ┌──── 按 host 共享(跨轨、跨 task) ────┐
 active task ─┤  lead(情报黑板, Redis)  credential(Redis)  │
 passive task─┤  lesson(经验教训, PG) ← 待重构为 RAG        │
              └────────────────────────────┘
              ┌──── 按 task 归集 ──────────────────┐
              │  finding(漏洞, PG, UNIQUE(task_id,dedup_key))│
              │  attackgraph(执行图, 按 task 投影, 不落表)   │
              └────────────────────────────┘
```

**lead 情报黑板机制（append-only episodic 记忆）：**
- **写**：`write_lead(kind, detail)` 工具，kind ∈ {clue 可疑点 / fact 既成事实 / deadend 死路}；身份值（host/hunter_id/source_task_id）闭包注入，LM 只填 kind+detail。
- **读**：无 read 工具——全量注入。顶层 agent 进 user prompt，子代理进 system prompt（子代理看不到 orchestrator 对话，靠黑板共享）。
- **收敛**：读时按 detail 精确去重（相同文本只留最新，不调 LLM）；每 host 保留最新 200 条（LTRIM）。
- **淘汰**：每次写滚动刷新该 host 的 TTL（默认 30 天，`lead_ttl_hours`）——持续写则续命，停写后自净。active/passive 一视同仁，不再分模式清理（同一 host 双模式不抢过期时间）。
- **张力备注**：lead 每 host 200 条上限、全量注入。若将来某超高频 host 在 TTL 内堆满 200 条一句话情报，全量 PUSH 会变贵——届时才需考虑转检索（当前 YAGNI）。

## 4. 两轨对照速查

| 维度 | ACTIVE | PASSIVE |
|------|--------|---------|
| 谁触发 | 人提 brief | 代理自动捕获流量 |
| assignment→task | 1→N(拆网站) | 1→1(一批流量一个) |
| task 里几个 agent | 主(orchestrator)+子(recon/exploit) | 单个(traffic-analysis) |
| agent 装配 | deep swarm(多 ChatModelAgent) | 单 ChatModelAgent |
| 流量表 | agent_traffic(自产, 挂 task) | proxy_traffic(捕获, 挂 host) |
| 流量与 task 时序 | 先 task, 后流量 | **先流量, 后 task** |
| 流量触发分析? | 否(防自激) | 是(就是分析对象) |
| 收尾函数 | finalizeTask(各 handler 内局部闭包，同名同义) | finalizeTask |
