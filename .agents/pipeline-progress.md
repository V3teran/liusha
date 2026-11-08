# Revamp Pipeline 进度

- 启动日期：2026-11-08
- 基线：main @ 34eb64f3
- 工作分支：pipeline/revamp

| # | 技能 | 状态 | commit | 结果摘要 |
|---|---|---|---|---|
| 1 | structure-overhaul | done | df39ddfc | 删 2 死文件 + 13 条失效 ignore；settingstore/toolstore 出 config/ 平级；10 个单领域 lib 模块归入 features；验证门（CI 口径）全绿。报告：`.agents/reports/1-structure-overhaul.md` |
| 2 | ui-ux-overhaul | done | 14ed31d2 | 设计体系收口：Token 对比度校准 + 语义色双事实源合一 + 分类色；FilterBar/Button/Select/DateTimeRange 统一 10 个列表页；响应式外壳 + 分组导航；修复聚焦辉光从未生效、漏洞来源筛选恒空、审计页控件遮挡、任务表截断。588 单测 + 102 张截图回归 + 11 项交互实测通过。报告：`.agents/reports/2-ui-ux-overhaul.md` |
| 3 | legacy-purge | pending | | |
| 4 | naming-overhaul | pending | | |
| 5 | quality-governance | pending | | |
| 6 | e2e-verification | pending | | |

## 跳过记录

（无）

## 操作记录

- 阶段 1 续跑时工作区混有一处报告外改动（README.md 免责声明改写），经用户确认丢弃后才提交。

## 遗留项

| 来源 | 项 | 转介 |
|---|---|---|
| 1 | `internal/assignment/store_integration_test.go` 引用已删除的 `SourceManual`，`-tags=integration` 编译失败（基线即有） | legacy-purge |
| 1 | `scripts/dev/e2e.sh` 清理不存在的 `session-store/`，用法注释描述已移除的 passive profile | legacy-purge |
| 1 | `deployments/README.md` 描述已删除的 `compose.dev.yaml` | legacy-purge |
| 1 | CI `integration tests` 步骤缺 `-tags=integration`，集成测试在 CI 中从不编译 | quality-governance |
| 1 | 各包测试把日志落盘到包目录 `logs/` | quality-governance |
| 1 | `pages/SystemConfig.tsx` 未遵循 `*Page` 命名 | naming-overhaul |
| 1 | `web/public/brand/` 下 4 个零引用 SVG 母版随 dist 公开发布，是否移出 public 待品牌负责人决定 | 用户决策 |
| 2 | `internal/finding/store.go:131,141` 注释仍写来源为 manual/auto（实际 self/proxy） | legacy-purge |
| 2 | 来源文案同一概念两种长度：任务表「自主」vs 漏洞页「自主下发」 | naming-overhaul |
| 2 | `WorkspacePage` 分包 688KB（CodeMirror 语言包全量打入），触发 Vite 500KB 警告（基线即有） | quality-governance |
| 2 | 代理模式对话/代理流量本地无数据，仅验证了空态 | e2e-verification |
| 2 | 探索图 React Flow 署名水印压在小地图边缘（库默认，保留署名） | 存疑 |
