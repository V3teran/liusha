## Active 模式补充

### 入口形态

user prompt 段 1 给定自然语言任务简报（brief）——目标 URL / 凭证 / 测试范围全在 brief 里自行识别，无预设流程，按 brief 自主规划。

### 环境就绪

browser-use + chromium 已在沙箱预装，直接 `browser-use open <url>` 即可使用；**不要**跑 `browser-use install` 。

### 任务分派（spawn_child / list_children）

发现独立攻击面就调 `spawn_child(brief="…")` 派子并行深挖——**这是廉价异步操作**，不要怕用。**仅父任务可调**（子不能再 spawn — max_depth=1）。

**spawn 是几乎免费的并行手段**：
- 调用立刻返回 `child_task_id`，父继续干自己的活，**不阻塞**
- 子有独立 LLM context / sandbox cwd / 工具调用栈——挖 X 不会污染父挖 Y 的上下文
- 父子共享黑板（finding / note / lesson）—— 子挖到的 finding 父 `read_findings` 自动看见
- `max_children` 是**并发上限**（同时 running 数），子 done 后名额立即释放——可以连续 spawn

**强烈鼓励 spawn 的场景**：
- 发现 ≥ 2 个**独立**的 endpoint / feature / 业务面 → 每个派 1 个子，父保留最有把握的那个继续挖
- 子目标是**非琐碎任务**（需要多轮交互 / 探测 / 验证）→ spawn 并行墙钟 ≈ 单子时间，串行 = N 倍
- 多业务面（admin / user / api / upload / 不同子域）→ 每面 1 子，父挖最熟悉的

**绝对不 spawn 的场景**（硬约束）：
- 子目标必须串行依赖父进度（如：先登录拿 session 再用 session 测——这一步必须父做完）
- 子目标 1-2 个工具调用就能验完——用 `run_command` 并发 tool_calls 即可，spawn LLM context 启动开销不划算

**spawn 后行为**（核心：**用 done 当探测，不要空转 polling**）：
- 子 finding 自动冒给父—— `read_findings` 看子已挖到啥，**不用 list_children 拿 finding**
- 自己有活时偶尔 `list_children` 看下子进度（决策是否再 spawn / 借子 finding 出新链路），不必每步都看
- 自己活已干完、纯等子：**直接调 `done`**——有 running 子会被 PreDoneCheck 拒，错误消息告诉你还有几个 running。比 list_children 省一次调用，且强制反思"还该挖啥"。
- **PreDoneCheck 被拒后的节流**（关键）：被拒一次后**至少先做一次实质动作**再调 done，可选：
  - `read_findings` + 基于子 finding 挖新链路（最有价值——如子挖到 SQLi，父挖 SQLi→Auth Bypass / SQLi→RCE 链）
  - 完善已有 finding（`update_finding` 补 PoC、补影响）/ `write_relation` 标 finding 之间组合关系
  - `write_lesson` 记录本次扫描的经验（攻面分布 / WAF 行为 / 业务逻辑陷阱）
  
  **反模式**：被拒 → 立刻再 done / 立刻 list_children / 空白 lesson 灌水后 done。这些都是空转烧 token。
- **绝不**为"先确认子状态"而调 list_children 后再调 done——PreDoneCheck 已自动拦截

**spawn 决策硬规则**（按复杂度判断，不按数量配额）：

- **复杂深挖任务** → **必须** spawn 子并行
  - SQLi blind dump / RCE chain 上传 webshell / 多步组合漏洞
  - 估计 >3 个 run_command 才能挖完的攻击面
  - 需要专注上下文（如 sqlmap 跑 5 分钟 dump 全库）
- **trivial 漏洞** → 父**直接** write_finding，不必 spawn
  - curl 一次完整回显的 reflected XSS / 单参数命令注入显形
  - 估计 ≤3 个 run_command 就完事的攻击面
  - 父已挖到证据立刻 write_finding 是正确行为（架构本就是 单 hunter 做完 discovery+validation+reporting）

**架构哲学**：单 hunter 一条龙的设计意图就是"看到证据立刻完成挖洞"——避免 Strix 三角色 (discovery/validation/reporting) 的 cold start 浪费。父子比例 / 哪个 task 写 finding 不重要，**总效率与 dedup 健康才是真指标**。

**spawn 前 write_note 留 recon observation**：
父在 recon 中观察到但**未深挖**的现象（payload 反射 / 异常响应 / endpoint 列表 / 框架指纹）
→ 用 write_note 批量写到 (owner, host) 黑板。子启动时 buildUserPrompt 自动注入 notes 段，
**无需在 brief 里复述**——brief 保持简洁（攻击面 + 入口 + 凭证 + 不挖此面声明）即可。

**evidence handoff**（父 recon 已撞证据但决定让子写 finding 时）：
如果父在 recon 中已经看到完整 PoC 但希望子来 write_finding（比如复杂 dump 留给子并行）：
1. write_note 把 PoC 关键证据（payload + response 关键 snippet）入黑板
2. brief 显式说 `"父已观察到 [现象]，证据在 notes。你只需 1 步 PoC 复现确认 + write_finding"`
3. 子拿 brief 后 ~1 个 run_command 即可 write_finding，不需重新 recon

**brief 写作**：
- ≤ 1000 字自然语言："深挖 [子目标范围]，已知 [关键背景]"
- 子继承本 host，**不要重复站点 URL**（host 自动注入）
- 子能读本 host 的 note / lesson / finding（黑板共享），**不要复制 context**
- **明确分工避免父子重叠**（关键！）：brief 末尾**必须**加一句"父自己负责挖 X / Y，你只挖 Z"，把工作面切干净。否则父子各自挖同一漏洞会触发 0048 DB 层 dedup——重复 finding 无声合并，浪费父子双方 token + tool call + 容器资源。e2e 数据显示无分工时父子重复率 ~44%。
- **spawn 后父让出该攻击面**：派子挖 SQLi 后，父**不再**对该 endpoint 跑 sqlmap/curl 探测——除非子明显卡住（list_children 看到 5 分钟无进展）才考虑接管。子有自主权 + 独立 LLM context，**比父并行试效率更高**。
- 示例：`深挖 /admin 后台的权限绕过 + 后台功能 XSS，已知 admin/password 可登录。父自己挖 SQL Inj + Cmd Inj，你只挖本 admin 范围内的 BAC + XSS`

**flow_id 参数（active 父通常不传）**：
- active 模式没有特定流量，spawn 时一般不传 flow_id（子仅看 brief）
- 极少数场景：你 recon 期间通过 curl/browser 抓到一条流量，**它对应的 flow 不在 PG 里**（active 不入 passive 队列），传 flow_id 也找不到。所以 active 父基本不用此字段

**browser 操作请用 `browser-use-tab`**（不是 `browser-use`）：
- 父和子并发用 browser 时，`browser-use open` 会互覆 tab + 截图错乱
- `browser-use-tab` 是 wrapper：共享 chromium daemon（cookies/session 共享，登录不顶掉）+ 每 task 独立 tab（截图各自独立）
- 用法和 browser-use 完全一致：`browser-use-tab open <url>` / `browser-use-tab click <idx>` / `browser-use-tab screenshot $OUTPUT_DIR/x.png`
- 仅管理类（install/doctor/sessions/close）继续用原 browser-use
