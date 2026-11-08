# Revamp Pipeline 进度

- 启动日期：2026-11-08
- 基线：main @ 34eb64f3
- 工作分支：pipeline/revamp

| # | 技能 | 状态 | commit | 结果摘要 |
|---|---|---|---|---|
| 1 | structure-overhaul | done | df39ddfc | 删 2 死文件 + 13 条失效 ignore；settingstore/toolstore 出 config/ 平级；10 个单领域 lib 模块归入 features；验证门（CI 口径）全绿。报告：`.agents/reports/1-structure-overhaul.md` |
| 2 | ui-ux-overhaul | done | 14ed31d2 | 设计体系收口：Token 对比度校准 + 语义色双事实源合一 + 分类色；FilterBar/Button/Select/DateTimeRange 统一 10 个列表页；响应式外壳 + 分组导航；修复聚焦辉光从未生效、漏洞来源筛选恒空、审计页控件遮挡、任务表截断。588 单测 + 102 张截图回归 + 11 项交互实测通过。报告：`.agents/reports/2-ui-ux-overhaul.md` |
| 3 | legacy-purge | done | 6c4d03e3 | passive/active 来源旧名全量收口 self/proxy（8 个标识符改名 + 落库文案 + LLM 工具描述 + SKILL.md）；清除退役角色/insight/flows/scanAdapter/mitmproxy 残留；ingest 路由更名；迁移 0197 修 schema 注释；修复集成测试编译失败与 e2e.sh 空参失效；术语门禁 +9 词。7 轮收敛（40→0→9→6→7→6→0），验证门全绿，集成测试 55/56 包（dispatch 基线缺陷）。报告：`.agents/reports/3-legacy-purge.md` |
| 4 | naming-overhaul | done | ad8cc8bc | 应用户追加要求下发来源 self→direct 全量替换（迁移 0198 + API + 前端，路由与取值同名）；SetDone→SetCompleted 等误导名；来源与任务控制文案单一真相源；接收者遗名、组件文件名/默认导出对齐惯例。7 轮收敛零发现，验证门全绿。报告：`.agents/reports/4-naming-overhaul.md` |
| 5 | quality-governance | done | 700f382d | 三路分波体检 38 条发现全部修复并配回归测试（代理转发截断/解压炸弹、工作区符号链接逃逸与并发覆盖、复现门交付顺序、拦截链超时、kill switch、流截断、前端迟到响应与截断覆盖等）；golangci v2 重写 + CI 补集成 tag/lint/web job；body_absent 迁移 0200 后删别名；WorkspacePage 688KB→13.8KB；测试日志不再污染包目录。验证门全绿（集成 race 全过、vitest 601）。报告：`.agents/reports/5-quality-governance.md` |
| 6 | e2e-verification | done | 37369434 | 前后端全栈实测 29 功能点（通过 25 / 阻塞 3 / 未测 1）：接口冒烟 56、端点探针 28、浏览器交互流 25、7 档视口 + 深色巡检、axe 0 违规。修复 LLM 配置错误任务永远运行中（四 agent 致命化 → aborted + 原因）、API 边界（非法 UUID 400 / 不存在 404 / source 校验 / 409）、过期深链提示、320px 标题挤压、WCAG AA 对比度与 grid 语义、manual/auto 命名残留。报告：`.agents/reports/6-e2e-verification.md` |

## 跳过记录

（无）

## 操作记录

- 阶段 4 期间用户追加要求：自主下发的 `self` 取值不符合业界惯例，重起名并全量替换 → 选定 `direct`（与既有路由同名），含 DB 迁移 0198。
- 阶段 4 期间用户要求重评来源中文名：多轮讨论后由用户定为「人工下发 / 代理捕获」；评估中发现并修复定时任务来源写错的缺陷（迁移 0199）。

- 流水线 6 阶段全部完成（2026-11-08）：最终检查点 37369434。

- 阶段 1 续跑时工作区混有一处报告外改动（README.md 免责声明改写），经用户确认丢弃后才提交。

## 遗留项

| 来源 | 项 | 转介 |
|---|---|---|
| 1 | ~~`store_integration_test.go` 引用已删除的 `SourceManual`~~ → 阶段 3 已修（零引用，集成测试全过） | 已解决 |
| 1 | ~~`scripts/dev/e2e.sh` 残留 `session-store/` 与 passive profile~~ → 阶段 3 已修（零命中） | 已解决 |
| 1 | ~~`deployments/README.md` 描述已删除的 `compose.dev.yaml`~~ → 阶段 3 已修（零命中） | 已解决 |
| 1 | ~~CI `integration tests` 步骤缺 `-tags=integration`~~ → 阶段 5 已修（并补 lint/术语门禁/web job） | 已解决 |
| 1 | ~~各包测试把日志落盘到包目录 `logs/`~~ → 阶段 5 已修（测试进程缺省关文件落盘） | 已解决 |
| 1 | ~~`pages/SystemConfig.tsx` 未遵循 `*Page` 命名~~ → 阶段 4 已改 `SystemConfigPage.tsx` | 已解决 |
| 1 | ~~`web/public/brand/` 零引用 SVG~~ → 阶段 6 补测时删除（favicon-small、logo-horizontal，全库零引用） | 已解决 |
| 2 | ~~`internal/finding/store.go` 注释来源旧名~~（阶段 3 已修；阶段 6 另清 7 处 `manual/auto` 残留并入术语门禁） | 已解决 |
| 2 | ~~来源文案两种长度~~ → 阶段 4 收口为单一真相源 `lib/source.ts`（全站「人工下发 / 代理捕获」） | 已解决 |
| 2 | ~~`WorkspacePage` 分包 688KB~~ → 阶段 5 编辑器与语言包懒加载，13.8KB | 已解决 |
| 2 | ~~代理模式对话/代理流量本地无数据~~ → 阶段 6 自建本地靶机经代理抓流量，批次 / 任务 / 会话 / 流量详情全链路实测 | 已解决 |
| 2 | 探索图 React Flow 署名水印压在小地图边缘（库默认，保留署名；阶段 6 已修其对比度 1.3→AA，位置未动） | 存疑 |
| 3 | ~~`executor/replayer.go` `body_absent` 别名~~ → 阶段 5 迁移 0200 改写存量键后删除别名 | 已解决 |
| 3 | ~~`internal/dispatch` 集成测试搭建未注入 conversation store，nil panic~~ → 阶段 4 追加 2 中已修复 | 已解决 |
| 3 | ~~`dispatch.Service` 方法接收者名 `a`~~ → 阶段 4 已改 `s` | 已解决 |
| 3 | 升级注意：ingest 路由更名后旧 sandbox 容器任务结束前抓流 404；SKILL.md 改动需 `make reseed`；`make e2e-active` → `make e2e-xss` | 发布说明 |
| 4 | 升级注意：API `source=self` → `source=direct`（无兼容别名，旧书签筛选为空）；需执行迁移 0198 | 发布说明 |
| 4 | ~~self→direct 改名与「人工下发 / 代理捕获」文案未经浏览器实测~~ → 阶段 6 浏览器实测通过（双 tab 路由、任务表 / 漏洞抽屉来源标签、来源筛选）；旧取值 `source=self` 现返回 400 | 已解决 |
| 4 | ~~调度器 `expandTrafficItem` 无写入方~~ → 阶段 5 整条路径删除 | 已解决 |
| 4 | 升级注意：迁移 0199 修正存量定时批次来源；`bac` SKILL.md 文案需 `make reseed` | 发布说明 |
| 5 | 升级注意：执行迁移 0200（断言键 body_absent→body_not_contains，无兼容别名）；测试进程缺省不写日志文件 | 发布说明 |
| 5 | ~~`.agents/governance.md` 建议稿~~ → 阶段 6 收尾时已入库（用户确认） | 已解决 |
| 6 | ~~LLM 相关阻塞~~ → 阶段 6 补测全通过；401 根因是 ambient `ANTHROPIC_AUTH_TOKEN` 泄漏进出站请求（客户端已擦除 + 回归测试），key 本身有效 | 已解决 |
| 6 | ~~定时任务到点触发未实测~~ → 阶段 6 补测：建模板到点克隆批次 source=direct、schedule_id 落库 | 已解决 |
| 6 | 升级注意：API 契约收紧（非法 UUID 400 / 不存在 404 / source 非法 400 / 技能重复 409）；LLM 配置错误任务直接落 aborted | 发布说明 |
