# 质量治理配置

> 仓库质量工作的领域锚点与验证命令单一事实源。质量治理（quality-governance）、端到端验证（e2e-verification）等技能开场先读本文件。
> 初始版本由 revamp-pipeline 六阶段（2026-11-08，df39ddfc…37369434）的事实基线沉淀，经用户确认入库。

## 领域锚点

- **下发来源**：`direct`（人工下发，含定时模板克隆）/ `proxy`（代理捕获）——DB CHECK、API、Go、TS、路由、中文文案（「人工下发 / 代理捕获」）全层同名。事实源：`internal/assignment/model.go`、`web/src/lib/source.ts`。禁止兼容别名；`scripts/lint-terminology.sh` 守门。
- **探索图写权限**：planner 写 objective/action；executor 写 observation；evaluator 只新建 result（唯一晋升门，`internal/evaluator/verifier.go`）。复现证据不走图，落 `exploration_verification` 审计链。
- **坐实交付**：evaluator `ClaimSink` → finding（`dedup_key` 幂等）；晋升顺序 = 落图 → 交付 → 标记假设（任一步失败假设保持 unverified，下轮重试）。
- **LLM 密钥双路径**：`llm_provider.encrypted_api_key`（前端直填，AES-GCM）→ 空则回退 `api_key_env` 环境变量。事实源：`internal/llmconfig/apikey.go`。anthropic 出站统一 `x-api-key`， ambient `ANTHROPIC_AUTH_TOKEN` 被客户端擦除（`client_pool.go`）。
- **API 错误契约**：非法 UUID / 非法 source → 400；资源不存在 → 404（`respondErr` 收敛 ErrNoRows）；唯一冲突 → 409；错误体 `{"error": "<中文消息>"}` 单出口 `internal/httpapi/errors.go`。

## 波次划分（全仓体检时按此分波）

1. **agent 核心**：`internal/{planner,executor,evaluator,monitor,cognition,framework,registry,tools,corpus,explorationgraph}`
2. **平台服务**：`internal/{dispatch,runner,proxy,ingestor,traffic,workspace,attachment,sandbox,httpapi,bus,logx,llmstore,llmconfig}` + `cmd/`
3. **前端**：`web/src`（pages 编排 / features 领域 / api 客户端 / styles token）

## 验证门（每波必过，CI 同口径）

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
gofmt -l $(find . -name '*.go' -not -path './vendor/*')
golangci-lint run ./...            # v2 配置；CI 钉 v2.15.0
scripts/lint-terminology.sh
go test -race -short ./...
go test -race -tags=integration ./...   # 需 Docker（testcontainers 实拉 pg）
cd web && npx tsc -b && pnpm build && pnpm test   # build 含类型检查（覆盖测试文件）
```

集成测试依赖起不来时列明跳过项，禁止静默跳过。浏览器验证用 `/tmp` 下一次性 Playwright 脚本（chromium for testing），页面全量巡检含 320/375/390/768/1024/1440/1920 视口 + 深浅主题 + axe-core WCAG 2 AA。

## 门禁防回流

- 术语门禁 `scripts/lint-terminology.sh`：旧来源取值（passive/self/manual|auto、直连/自主下发）、退役角色与包名、body_absent 等，新增禁用词直接加 FORBIDDEN 数组。
- CI（`.github/workflows/ci.yml`）：test job（vet/gofmt/golangci/术语/单测/集成 -tags=integration）+ web job（tsc/vite/vitest）+ build job（依赖前两者）。
