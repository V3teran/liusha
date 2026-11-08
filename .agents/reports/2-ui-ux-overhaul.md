# 阶段 2/6 · ui-ux-overhaul 交付报告

基线：df39ddfc（阶段 1 之后）· 分支 pipeline/revamp · 2026-11-08

## 设计方向

**保留并加固既有方向，不推倒重来**：前端已有一套完整的「克制的深色优先操作台」体系（OKLCH Token、双主题、Geist 字体、蓝灰强调色），上一轮流水线也已清理过一遍无效 CSS。本轮的判断依据是实际运行截图（两主题 × 390/900/1024/1440 四档宽度，17 条路由）：视觉方向本身没有问题，问题集中在**体系没有被一致执行**——同一类元素存在多套写法，Token 有悬空引用和第二事实源，响应式完全缺失。所以本轮做的是「收口与补全」，而不是换风格。

参考的业界原则（只提炼原则，不照搬方案）：安全/可观测类操作台的信息架构按任务域分组导航；列表页统一「页头 → 指标 → 筛选栏（左控件 / 右分页）→ 内容」的版式；表单控件分 32/36px 两档密度；语义色以文字对比度为约束（WCAG AA 4.5:1）；分类色与语义色正交。

## 核心问题与关键改进

| # | 问题（实测证据） | 改进 |
|---|---|---|
| 1 | **窄屏不可用**：390/768 宽度下侧栏仍固定 256px，内容区被挤成一条 | 响应式三档：≥1024 侧栏可收起（偏好持久化）/ 768–1023 强制图标栏 / <768 侧栏收入抽屉（Radix Dialog：焦点锁定、Esc、跳转自动关闭、自左滑入且尊重减弱动效）；新增 `useMediaQuery` |
| 2 | **聚焦辉光从未生效**：`focus:glow-accent`（22 处）在产物中零规则——`.glow-accent` 是普通 CSS 类，Tailwind v4 只为 `@utility` 生成变体 | `utilities.css` 中改为 `@utility glow-accent`，一处根治；产物与实测 computed style 均已验证 |
| 3 | **浅色主题文字对比度不达标**：9 个语义色作文字时 2.0–3.6:1（黄色「中危/待处理」仅 2.0:1）；角色/身份色为深色底调校的 400 系硬编码 | 浅色语义 Token 按 OKLCH 换算重新校准（纯文字 ≥6:1、软徽章 ≥4.5:1，色相不变）；深色 `status-neutral` 提亮；新增双主题分类色 `--cat-*`（同样校准）。对比度用脚本按 sRGB gamma 空间混合实算，非估计 |
| 4 | **语义色双事实源**：严重度/任务状态/漏洞状态在 CSS Token 与 TS 十六进制各有一份，后者不随主题 | `severity.ts` / `taskStatus.ts` / `findingStatus.ts` 改为引用 Token；智能体身份色、消息角色色、审计指标色改引 `--cat-*`（新 `lib/categoricalColor.ts`）；`severityTagColor` 透明度改用 `color-mix` |
| 5 | **悬空 Token（3 处）**：`var(--muted)`（HTTP 3xx/GET 色退化为继承色）、`var(--color-muted)`、`bg-muted`（对话页「正在思考」三个圆点透明不可见） | 全部改为现存 Token；2xx 按注释原意改为 `--status-success`。另做了一次全库 `var()` 引用与定义比对，确认清零 |
| 6 | **列表页工具栏各自为政**：分页一会儿同行一会儿换行；LLM 审计页搜索框（内 `w-72` 外套 `w-64`）**覆盖日期框** | 新 `FilterBar`：左控件组可换行、右附属区钉在首行右端、窄屏上下堆叠、`role=search` 地标、统一「清空筛选」；10 个页面全部迁移，筛选栏移出滚动区常驻 |
| 7 | **表单控件 5 套写法**：圆角 md/lg 混用、聚焦一半辉光一半 ring、占位符二/三级灰混用；12 处原生 select 各自手写 | `control.ts`（框体 + sm/md 两档 + `INPUT_CLASS`/`TEXTAREA_CLASS`/`FIELD_LABEL_CLASS`）+ `Select`（原生语义 + 自绘箭头）+ `DateTimeRange`；全库原生 `<select>` 清零 |
| 8 | **主按钮两种相反的悬停逻辑**（一半悬停变浅 + 描边），高度 4 种、字号 13px/14px 混用 | `Button`（primary/secondary/ghost/danger × sm/md，悬停一律加深，与控件同高）；页头主操作、弹窗/抽屉/设置区的主次按钮全部迁移（10 处主 + 配套次按钮） |
| 9 | **任务表 1440 宽度下截断**「结束时间」「操作」列（列设计宽度合计 1250 > 内容区 1136） | 目标列设计宽度 320→200（宽屏按权重拉伸，截断 + title 悬停全文） |
| 10 | **漏洞来源筛选功能失效**：选项值 `manual/auto`，DB 约束只允许 `self/proxy`，选任何来源结果恒为空（测试把错误值固化成了预期） | 选项值改为 `self/proxy`，文案与表格列统一；测试改为断言正确取值；实测请求带 `source=proxy`、「自主下发」返回 3 条真实数据 |
| 11 | 侧栏 14 项平铺无分组 | 「作业 / 观测 / 配置」三组（`layout/navigation.ts`），组名 `role=group` 可读屏；图标栏态以分隔线代替组名 |
| 12 | 探索图首屏缩放至 ~0.1，节点完全不可辨认 | 静态全图适配加可读下限 0.6（鸟瞰交给已有的小地图）；运行中跟随前沿的逻辑不变 |
| 13 | 空壳路由 `/dashboard`（「总览·建设中」，全库零入口）；无兜底路由 | 删除空壳；新增外壳内 404 页（侧栏可继续导航） |
| 14 | 零散不一致 | 任务页补副标题；漏洞页首屏整页转圈改为表格骨架屏、概况卡窄屏换行；配置类页面统一 `max-w-4xl`；`ConfigListShell` 的 emoji 空态/错误态改为图标 + `role=alert/status`；表单标签两套样式合一 |

## 改造范围

- **页面（17 条路由全部检查）**：任务、对话（自主/代理）、漏洞、代理流量、LLM 审计、探索图、工作空间、凭证库、智能体、工具、技能、LLM 配置、系统配置、日志、登录、404；抽屉/弹窗：新建任务、修改密码、漏洞处置、配置抽屉、LLM 调用详情、手机导航抽屉
- **全局**：`design-tokens.css`（语义色校准 + 分类色）、`utilities.css`（glow 工具类注册）、`index.css`（抽屉动画 + 减弱动效）
- **新公共件**：`FilterBar`、`ui/Button`、`ui/Select`、`ui/DateTimeRange`、`ui/control.ts`、`hooks/useMediaQuery`、`layout/Sidebar`、`layout/navigation`、`lib/categoricalColor`、`lib/datetime`、`pages/NotFoundPage`
- **删除**：`pages/PlaceholderPage.tsx`；`ConfigListShell` 内仅测试可达的客户端分页 + `PagerBar`；两个筛选栏中重复的日期换算函数；`ConfigDrawer` 导出的旧 `INPUT_CLASS` 与三个弹窗各自的本地副本；`useLlmAuditFilters` 中失去用途的 `EMPTY_FILTERS`

## 有意保留（判断为合理，不改）

- 探索图节点/边配色（`kindTheme.ts`、`SEVERITY_DOTS`、`EDGE_COLORS`）：注释记载了按双主题 ≥3:1 图形对比度校准的依据，实测成立
- 原生 `datetime-local`：显示格式随浏览器语言；自研/引入日期选择器需新增依赖且收益有限
- `Badge` 实心变体的 `#ffffff`：彩色底上的前景白字，属有意的 on-color
- 头像按钮 `hover:bg-accent-softer`：头像控件，不属于按钮体系

## 验证结果

| 项 | 结果 |
|---|---|
| `tsc -b`（含 noUnusedLocals） | ✅ |
| `pnpm test` | ✅ 72 文件 / 588 用例（基线 67 / 552；新增 Select/DateTimeRange/FilterBar/Button/useMediaQuery/datetime/ConfigListShell/AppShell 响应式用例，TDD 先红后绿） |
| `pnpm build` | ✅ 主包 117KB gzip（与基线持平） |
| 产物核验 | `.focus\:glow-accent` / `.hover\:` / `.focus-within\:` 均已生成；`--cat-*`、`.app-drawer` 在产物中 |
| 实机截图回归 | 真实 api + 真实数据，17 路由 × {1440 双主题, 1024 浅, 900 深, 390 双主题}，共 102 张；零控制台错误、零 5xx、零整页横向溢出、零工具区越界（新增越界检测：捕获被 overflow-hidden 裁掉的溢出——首轮据此发现并修复了手机端日期范围被裁） |
| 交互实测（Playwright） | 11/11 ✅：手机抽屉跳转并自动关闭；下拉聚焦辉光为非透明阴影；漏洞来源筛选请求带 `source=proxy`；「自主下发」返回 3 条真实行；漏洞/LLM 审计「清空筛选」一次复位（含搜索词，单次 URL 更新）；新建任务弹窗 Esc 关闭；`/dashboard` 落外壳内 404；探索图静态首屏缩放 0.6；三个抽屉在浅色主题下打开无运行时错误 |
| Go 侧 | 本阶段零改动（改动面全部在 `web/src`） |

**未能实测**：代理模式对话页、代理流量页本地无代理流量数据，只验证了空态；`proxy` 来源的漏洞同理。

## 遗留 / 转介

| 项 | 转介 |
|---|---|
| `internal/finding/store.go:131,141` 注释仍写来源为 `manual 主动下发 / auto 被动代理`（实际为 self/proxy） | legacy-purge |
| 来源文案：任务表列为「自主」、漏洞表与筛选为「自主下发」，同一概念两种长度 | naming-overhaul |
| `WorkspacePage` 分包 688KB（240KB gzip，CodeMirror 语言包全量打入），触发 Vite 500KB 警告（基线即有） | quality-governance |
| 探索图右下角 React Flow 署名水印压在小地图边缘（库默认行为，保留署名） | 存疑，未改 |
