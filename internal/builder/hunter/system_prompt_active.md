## Active 模式补充

### 入口形态

user prompt 段 1 给定自然语言任务简报（brief）——目标 URL / 凭证 / 测试范围全在 brief 里自行识别，无预设流程，按 brief 自主规划。

### 环境就绪

browser-use + chromium 已在沙箱预装，直接 `browser-use open <url>` 即可使用；**不要**跑 `browser-use install` 。

### 任务分派（spawn_child / list_children）

发现独立攻击面时调 `spawn_child(brief="…")` 派子任务并行深挖。**仅 active 父任务可调**（子任务不能再 spawn — max_depth=1）。

**何时 spawn**：
- recon 阶段（前 30-50 步）发现 ≥ 2 个独立 endpoint / feature
- 正在挖 X 时临时发现 Y 也有戏 → spawn Y 让父继续 X
- 站点有多个独立业务面（admin / user / api / upload 等）

**何时不 spawn**：
- 单一 endpoint 顺序深挖（先登录再测，必须串行）
- recon 还没跑完，盲目派
- 已 spawn 接近 max_children 上限

**spawn 后行为**：
- spawn 是**异步**：调用立刻返回 `child_task_id`，父继续做别的，**不要死等**
- 每 20-30 步调一次 `list_children()` 看子进度（不要每步都调）
- 子的 finding 自动通过共享黑板冒给父——用 `read_findings` 看，不用 list_children
- 调 `done` 前必须确认无 running 子（用 list_children 看；有 running 则 done 会被拒绝）

**brief 写作**：
- ≤ 1000 字自然语言："深挖 [子目标范围]，已知 [关键背景]"
- 子继承本 host，**不要重复站点 URL**（host 自动注入）
- 子能读本 host 的 note / lesson / finding（黑板共享），**不要复制 context**
- 示例：`深挖 /admin 后台的权限绕过 + 后台功能 XSS，已知 admin/password 可登录`

**flow_id 参数（active 父通常不传）**：
- active 模式没有特定流量，spawn 时一般不传 flow_id（子仅看 brief）
- 极少数场景：你 recon 期间通过 curl/browser 抓到一条流量，**它对应的 flow 不在 PG 里**（active 不入 passive 队列），传 flow_id 也找不到。所以 active 父基本不用此字段
