# 阶段 1/6 · structure-overhaul 交付报告

基线：main @ 34eb64f3 · 分支 pipeline/revamp · 2026-11-08

## 删除清单

### 被跟踪文件

| 文件 | 类别 | 证据 |
|---|---|---|
| `internal/configstore/README.md` | 过时设计文档 | 全库零引用；正文描述不存在的 `internal/cache` 包、不存在的方法（`ExecutorByID`/`ComplexityByCode`/`ListExecutors`）、与代码不符的缓存键（代码实际为 `configstore:agent:*`）、链接已删除的 `docs/*.md`。包 godoc 已覆盖真实语义。 |
| `web/public/brand/safari-pinned-tab.svg` | 重复资源 | 与 `web/public/safari-pinned-tab.svg` 逐字节相同（`cmp` 一致）；全库零引用；位于 public/ 会被原样打进 dist。 |

### 失效 ignore 规则（`.gitignore` / `.dockerignore`）

| 规则 | 证据 |
|---|---|
| `/agent-worker`（两处） | `cmd/agent-worker` 不存在且 git 历史中无此目录，无生产者。 |
| `/liusha-runner`（两处） | 全库无 `-o liusha-runner` 构建；`go build ./cmd/runner` 产物名为 `runner`（已有规则覆盖）。 |
| `/session-store/`（两处） | 注释声称的生产者 ResultCompress middleware 已不在代码中（`grep -i 'ResultCompress\|result_compress'` 零命中）。 |
| `/asynq.log` `/runner.log` `/e2e-test.log` | 无任何重定向生产者；dev 日志统一写 `logs/`（`scripts/dev/run-svc.sh`），已由 `/logs/` 覆盖。 |
| `/docs/Tsec-Hackathon-main/` `/docs/hks2.md` `/docs/无径之径*` | `docs/` 目录不存在；条目是手工放置的外部参考资料，无生产者；且注释点名外部项目。 |
| `.dockerignore: core.test executor.test` | 只列了两个包的测试二进制，泛化为 `*.test`（与 `.gitignore` 同源）。 |

补齐：根目录 cmd 二进制清单与 `cmd/` 实际入口对齐，新增 `/reseed`、`/sandbox-server`（两份 ignore 同步）；`/corpus-import` 从「补充」段并入主清单。

### 本地产物（gitignored，零 git 风险）

`internal/{cognition,credential,dbtest,dispatch,executor,finding,httpapi,llminvocation,tools}/logs/`——各包测试运行时落盘的日志目录（`*.test.log`）。

## 结构搬迁清单

| old → new | 理由 | 影响面 |
|---|---|---|
| `internal/config/settingstore` → `internal/settingstore` | 不依赖 `config`（只依赖 `cachestore`），是 system_setting 表的 DB+缓存层，与同模式的 `configstore`/`llmstore`/`skillstore`/`cachestore` 平级安放；嵌在 `config/`（YAML 加载器）下混淆了静态文件配置与运行期 DB 配置。 | 9 个文件改 import |
| `internal/config/tool`（package `tool`）→ `internal/toolstore` | tool 表持久化层，不依赖 `config`；与 `skillstore`（Skill 表持久化层）同模式；原包名 `tool` 与运行期 `internal/tools` 易混。类型无口吃：`toolstore.Store/Definition/Kind/ListParams/Reconcile`。 | 3 个文件（调用方原别名 `cfgtool` 去除）；`cmd/api` 局部变量 `cfgToolStore` → `toolCatalog`（与注入字段 `ToolCatalog` 对应，避开既有 `toolStore` = toolinvocation） |
| `web/src/lib/{findingEvidence,findingStatus}` → `features/finding/` | 仅 finding 领域消费 | 2 文件 |
| `web/src/lib/{llmInvocationStatus,llmMessage,llmTiming,toolCalls}` → `features/llm-audit/` | 仅 llm-audit 消费 | 4 文件 |
| `web/src/lib/{messageKind,threadRows}` → `features/conversation/` | 仅 conversation 消费 | 1 文件 |
| `web/src/lib/{rawMessage,httpStatus}` → `features/traffic/` | 仅 traffic 消费 | 3 文件 |

前端搬迁依据：features 已有领域辅助模块先例（`kindTheme.ts`、`roles.ts`、`toolMeta.ts`、`listText.ts`、`agentsEditor.ts`）；`lib/` 为跨 feature 共享层。feature 内引用按既有惯例改为 `./x`（71 处先例、0 处自引别名），跨层引用 `@/api/...`。测试文件随模块同搬。

留在共享层（通用基础设施，非领域）：`lib/markdown`、`hooks/useEventStream`、`hooks/useTypewriter`、`hooks/useCodeCopy`、`components/StatChip`、`components/CopySection`、`components/ui/*`。

## 存疑未删

| 对象 | 现状 | 为何不删 |
|---|---|---|
| `web/public/brand/{apple-touch-icon,favicon-small}.svg` | 零引用；注释自述为 `apple-touch-icon.png` / `favicon-16.png` 的渲染母版 | 设计源文件，非产物；但放在 public/ 会随 dist 公开发布——建议移出 public（如 `web/design/brand/`），由品牌负责人决定 |
| `web/public/brand/logo-horizontal.svg` | 零引用；v10 横向组合标，注释引用已删除的 `mark.svg` | 品牌资产，用途可能在仓库外 |
| `web/public/brand/app-icon-dark.svg` | 仅 `index.html` 注释提及 | 深色对照母版 |
| `.gitignore`/`.dockerignore: $CLAUDE_JOB_DIR` | 字面量目录名 | 防护某工具未展开环境变量时生成的字面目录，生产者在仓库外，保留 |

## 承载性文件确认清单（查过、是活的，下轮免检）

- `db/migrations/0196_baseline.{up,down}.sql`——golang-migrate 按文件名加载（Makefile `migrate`、single/api Dockerfile COPY）
- `agents/*.md`、`skills/**/SKILL.md`——api 启动 insert-only 种子 + `cmd/reseed` 覆盖导入；Dockerfile COPY
- `config/config.yaml`——三进程运行时配置；compose 挂载 + Dockerfile COPY
- `vendor/`——Dockerfile `-mod=vendor` 构建
- `web/pnpm-lock.yaml`——nginx/single Dockerfile `--frozen-lockfile`
- `deployments/tool-images/sandbox/*`——build-sandbox CI + `Dockerfile.final` COPY；`tools.yaml` 被 api/runner 镜像 COPY
- `cmd/corpus-import`——expert corpus 唯一写入入口（agent 运行期只读 `search_corpus`）
- `cmd/e2e`、`scripts/dev/*.sh`、`scripts/lint-terminology.sh`——Makefile 目标
- `.gitmessage`（`git config commit.template`）、`.gitleaks.toml`（security.yml）、`.golangci.yml`、`release-please-*`（release-please.yml）、`web/components.json`（shadcn CLI）
- `web/public` 中被 `index.html` / `AppShell` / `LoginPage` 引用的图标
- `web/src/test-setup.ts`——`vite.config.ts` `setupFiles`

## 转介后续阶段（范围外，未处理）

| 项 | 转介 |
|---|---|
| `scripts/dev/e2e.sh:86-88` 清理已不存在的 `session-store/`；文件头用法注释仍描述已移除的 passive profile | legacy-purge |
| `deployments/README.md` 描述已合并删除的 `compose.dev.yaml` | legacy-purge |
| `internal/assignment/store_integration_test.go` 引用已删除的 `SourceManual`（7798736e 遗留），`-tags=integration` 编译失败 | legacy-purge |
| CI `integration tests` 步骤缺 `-tags=integration`，集成测试文件在 CI 中从不编译（上条因此未被发现） | quality-governance |
| 各包测试把日志落盘到包目录 `logs/`（根因在测试日志配置） | quality-governance |
| `pages/SystemConfig.tsx` 未遵循 `*Page` 命名 | naming-overhaul |

## 验证门

| 项 | 命令 | 结果 |
|---|---|---|
| Go 构建 | `go build ./...` | ✅ |
| vet | `go vet ./...` | ✅ |
| gofmt | CI 同款 `gofmt -l`（排除 vendor） | ✅ 零差异 |
| golangci-lint | `golangci-lint run ./...` | ✅ 0 issues |
| 术语门禁 | `scripts/lint-terminology.sh` | ✅ |
| Go 单元测试 | `go test -race -short ./...` | ✅ 47 包全过 |
| 前端构建 | `pnpm build`（`tsc -b && vite build`） | ✅（仅 chunk >500kB 体积警告，基线即有） |
| 前端测试 | `pnpm test` | ✅ 67 文件 / 552 用例全过 |
| 集成测试编译检查 | `go vet -tags=integration ./...` | ⚠️ 仅 `internal/assignment` 失败（`SourceManual` 未定义）；已在干净 HEAD 的临时 worktree 复现，属基线遗留、非本阶段引入，已转介 legacy-purge |

集成测试本体（需 Postgres/Redis）未运行：CI 的 `integration tests` 步骤缺 `-tags=integration`，带 tag 的文件在 CI 中从不编译，已转介 quality-governance。

## 收尾复扫（续跑轮）

切入点：配置面 + 旧路径残留 → 空目录/零字节/游离产物 → 前端共享层消费方映射。

- 旧路径（`internal/config/{tool,settingstore}`、`cfgtool`、已搬迁的 10 个 `lib/*` 模块、已删两文件）全库残留引用：零
- 零字节跟踪文件、跟踪的日志/二进制/临时文件：零
- 单测重新生成的 `internal/*/logs/`：已再次清理（根因已转介）
- `lib/` 剩余模块均为跨 feature 共享；`lib/markdown` 单一消费方但属通用渲染设施，维持原判

结论：连续一轮零新发现 + 验证门（CI 口径）全绿，阶段退出。
