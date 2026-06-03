# 攻击面迁移：手动 endpoint 表 → http_flow 派生（strix 单一真相源模型）

状态：进行中（2026-06-03 起）
决策：用户拍板"现在就做全套迁移"。对照 strix（Caido 全流量 store + 派生 sitemap）后确认方向。

## 背景与动机

复盘一次 active:bac e2e 暴露：
- `endpoint` 表只有 method+path，**没有参数**（GET query 在 url、POST body 在 request_body，只有 http_flow 有）
- commander 靠**手动 `write_endpoint` 转写**recon 所见 → 易漏（这次就漏了参数维度）
- endpoint（手动广覆盖）与 http_flow（浏览器基线）对"浏览过的路由"**双写冗余**

strix 模型：所有工具流量走 Caido 代理 → 一个 raw store；**sitemap 是从流量去重派生的树**（fuzz/重复自动塌缩）；agent 按 request_id 引用、`repeat_request` 重放。无手动 endpoint 表。

## 目标终态

- CLI/recon 工具流量经代理入 `http_flow`（source=internal，owner 级归属）
- 攻击面 graph/sitemap **从 http_flow 派生**（templatize 去重 + 静态过滤），不再手动维护
- 退役 `endpoint` 表、`write_endpoint` 工具；`read_endpoints` → `list_sitemap`
- 参数自动入库（解 endpoint 无参数）；单一真相源；消灭手动转写

## 关键设计决策

### 归属（最硬的点，已解）
- 容器 per-run 共享（commander+striker），spawn 时绑 commander 的 hunter_id（`launcher.Spawn(ctx, p.HunterID)`）
- CLI 流量按 owner 级归属：用容器绑定的 hunter_id → ingestor `handleInternalSnap` 反查 owner（现有机制）
- hunter_id 对 CLI 流量统一记 commander 的（owner 正确，graph 是 owner-scoped，够用）

### fuzz 污染治理（用户核心顾虑）
- **分层**：http_flow（raw）全收利于 replay；graph（派生）去重过滤
- **入库去重**：按 `(owner, method, templatize(path))` 去重，每路由留首条代表请求（带参数）→ sqlmap 喷 1 万 `/x?id=FUZZ` → 模板化 `/x?id=:val` → 一条。治膨胀 + 治污染
- CLI 版 CAPTURE_TYPES：丢静态资源类请求

### CA / 代理（mitmproxy）
- 容器内跑 mitmproxy 作 CLI 流量 MITM；容器需信任 mitmproxy CA（装进镜像信任链）
- launcher 注入 `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY` 指向容器内 mitmproxy（strix 同款，标准 env，CLI 工具自动尊重，无需 per-tool skill）
- 复用现有 `/internal/v1/flows/ingest`（browser-svc.py 已用此路径，零 cmd/proxy 改动）

## 分阶段执行（每阶段独立可验/可回退）

### Phase 1 — CLI 流量入 http_flow（mitmproxy 方案，纯增量，不删）

**不复活 proxify internal listener**（v34 删的复杂度：Proxy-Auth/407/HTTPS session-stash/per-hunter，容器 per-run 共享根本不需要 per-hunter）。
**改用 mitmproxy + 复用现有 ingest**——镜像 browser-svc.py 已验证的模式（容器内服务 → POST /internal/v1/flows/ingest）。这也正是项目原定的"待做 mitmproxy"。

1. pentools 镜像装 mitmproxy（python3 已在，pip 装）+ 其 CA 装进容器信任链
2. 写 mitmproxy addon：每条 response → POST /internal/v1/flows/ingest（hunter_id 从容器 env 取，owner 级归属）
3. **fuzz 去重在 addon 源头**：addon 内存维护 per-run 已见集合 `(method, templatize(path))`，重复直接不 POST（流量没入库就掐断；sqlmap 喷 1 万 → 1 条）+ 丢静态资源类
4. launcher 注入 `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY` → 指向容器内 mitmproxy + 注入 hunter_id env
5. 容器内启动 mitmproxy（PID 1 sandbox-server 或 entrypoint 拉起）

优势：零 cmd/proxy 改动、复用现有 ingest+handleInternalSnap、无 Proxy-Auth 复杂度、和 browser-svc 同款架构。
- 验证：recon 后 CLI 请求（带参数）进 http_flow、fuzz 不膨胀（addon 去重）、owner 对。endpoint 表照旧。

### Phase 2 — graph 从 http_flow 派生（增量，两套并存）
5. graphview projector 从 http_flow（templatize 去重）派生攻击面节点
6. 新 `list_sitemap` 工具读派生攻击面；read_endpoints 暂留
- 验证：flow 派生图 ≈ 旧 endpoint 表，参数齐

### Phase 3 — endpoint 表退役（破坏性，最后）
7. 删 write_endpoint；read_endpoints → list_sitemap
8. drop endpoint 表（migration）
9. 改 prompts：commander.md（recon 不再 write_endpoint，改"走全 → 流量自动成图"）、striker.md、BAC skill（read_endpoints→list_sitemap）
- 验证：全链路 active:bac e2e

## 风险 / 回退
- Phase 1/2 纯增量，出问题直接不注入 HTTP_PROXY env（mitmproxy 不参与）/ 回退 projector，endpoint 表仍在
- Phase 3 前确保 Phase 2 派生图已验证等价，再 drop 表（migration 可保留表数据备查）
- 不复活 v34 删的 proxify internal listener（Proxy-Auth/407/per-hunter 复杂度）；改 mitmproxy + 复用 ingest，owner 级归属，避开旧脆弱路径
