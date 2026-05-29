# browser-use 回归原生 --session 会话管理 设计

**状态**: 设计中
**日期**: 2026-05-29
**作者**: V3teran
**关联**: [2026-05-16-sandbox-server-design.md](2026-05-16-sandbox-server-design.md)（容器执行模型）；v35 `0c06e10a`（砍 CDP capture）

---

## 背景

### 问题现象

active e2e 反复出现：commander/striker 首次 `browser_use open` 成功（~8s），**之后所有 browser_use 调用全部卡满超时**（state/click/eval，甚至重新 open 都 60s timeout），最终靠 curl fallback 才让 e2e PASS。浏览器能力实际不可用。

### 本次调查证据（2026-05-29，留容器 `LIUSHA_KEEP_SANDBOX=1` 现场复现）

跑 `e2e.sh active:xss`，scanner.log + tool_invocation 表实测：

| step | 动作 | duration | 结果 |
|---|---|---|---|
| open login.php | 7.7s | `{"exit_code":0}` + 截图 ✅ |
| state | 15.0s | `{"exit_code":-1,"timed_out":true}` ❌ |
| open login.php（LLM 重试） | 60s | timed_out ❌ |
| open xss 页 | 60s | timed_out ❌ |
| open login.php | 60s | timed_out ❌ |
| open xss | 60s | timed_out ❌ |

即首个调用后，**共享 chromium daemon 对后续所有 browser-use-cli 连接都不响应**。容器内观察：

- chromium daemon（PID 19）**始终活着**，CDP `/json/version` 也返 200——卡的不是 chromium 进程本身。
- `sandbox-server`（PID 1）超时时**杀整个进程组**（`exec.go` `Setpgid` + `kill(-pgid, SIGKILL)`），browser-use-cli 确实被杀，但留下 **13 个 `[browser-use]/[flock] <defunct>` 僵尸**（PID 1 不回收孤儿）。
- 手动直连 `browser-use-cli --cdp-url ... state` 在刚跑完时**秒回**，多跑几次后**也开始 70s 超时**——说明共享 daemon **随每次 `connect_over_cdp` 单调劣化，攒到一定量后永久锁死，不自愈**。

### 根因

现在的 wrapper（v29-v33 CDP-capture 期引入）**手动起一个常驻 `chromium --remote-debugging-port=9222`**，每次 browser_use 调用都新起一个 browser-use-cli 经 **`--cdp-url` 反复 `connect_over_cdp`** 连它。这个共享 daemon 在反复 CDP attach（叠加 SIGKILL 留下的悬挂 session）下劣化锁死。

**v35 已经砍掉 CDP capture**——chromium 不再需要走 proxy 抓包——这个手动 daemon + `--cdp-url` 已**失去存在理由，纯属负债**。

### 原模型可行性验证（同容器实测）

browser-use-cli **自带原生 `--session` 持久会话管理**（`--session NAME` / `close`=停 daemon / `sessions`=列会话）。让 browse-use 自己托管 chrome：

```
[open login.php]  21s  ✅（browse-use 自启 chrome daemon，冷启）
[state]            1s  ✅ 状态保持（看到页面）
[state]            0s  ✅
[open xss_r/]      1s  ✅ 复用同一浏览器
[state]            0s  ✅
[state]            0s  ✅  —— 6 次零劣化，无 rot
```

多 tab 也验证：`window.open` 新建 tab，`switch 99999` 报 `Available: 0-1`，`switch 0/1` 自由切——**一个 `--session` 内多 tab + 跨进程状态保持完全成立**。

Dockerfile 注释（Layer 2.5）本就写明设计意图：「session 自动管理，chrome 生命周期由 browser-use 内部托管，**无需我们 spawn**」。手动 daemon 是 CDP-capture 期对原设计的偏离，本次回归原设计。

---

## 目标

- browser_use 回归 browse-use 原生 `--session` 会话管理，**删除 wrapper 手动 chromium daemon + `--cdp-url`**。
- 保留现有 `browser_use` 工具接口（离散 action：open/state/click/input/wait/eval/extract/source/reset）——**hunter LLM 逐步驱动**：open→自动截图→LLM 看图决定→click/input→再截图→…直到目标达成（如登录）。
- 保留多 agent 共享模型：**一 host 一 active 任务 = 一个共享浏览器**，commander 占 tab0，striker `window.open` 新 tab。
- 跨离散调用**状态保持**（页面活着）、**无 rot**。
- 修 sandbox-server（PID 1）不回收僵尸的次要 bug。

## 非目标

- 不把 browser_use 改成 browse-use 自主 agent（「一次调用 = 一个完整 NL 任务」）——LLM driver 仍是我们的 hunter LLM，离散 action 不变。
- 不动 `browser_use.go` 工具的 action 枚举 / 参数 schema / grounding 坐标换算（接口零变化）。
- 不恢复 CDP capture / 流量入字典（v35 已收口，凭证共享走 redis）。

---

## 修复设计

### 浏览器生命周期 + 身份模型：`--session` = 身份（cookie jar）

实测确认（2026-05-29 同容器）：`--session A` 种的 cookie，`--session B` 读不到，且各自独立 chromium daemon 并发。故 **session = 身份（cookie jar 边界）**：

- **同一身份**：`SESSION = ${IDENTITY:-default}`。commander+striker 共用一个 `--session <身份>` 浏览器，登录态共享，**tab 隔离**页面/截图/并发。
- **多身份（越权/BAC）**：不同 `IDENTITY` → 不同 `--session` → 独立浏览器（独立 cookie jar，按需多起 chromium）。身份数 = 同时在用的账号数（常态 1，BAC 2-3），**不是** agent 数。单/多账号的越权判断逻辑在 `vuln/bac` skill。
- 首个命令由 browse-use 自启 chrome（冷启 ~20s）；后续跨 CLI 进程复用、状态保持。`browser_use` 工具新增可选 `identity` 字段（= 凭证 name），缺省走共享 `default`。

### 共享 + tab 隔离模型（同一身份内）

- **谁先 open 谁占 tab0**（role-agnostic，commander/striker 皆可——实际不一定 commander 先碰浏览器）。
- **后来者新 tab**：用 `window.open(目标URL)` **一步建+导航**新 tab（实测：先 `window.open(about:blank)` 再独立进程 `open` 会让空 tab 被回收塌缩；直接带 URL 开则稳定）。tab 号记到 `/tmp/browser-tab-${SESSION}-${TASK_ID}.idx`。
- 每次后续 action 先 `switch $TAB_IDX` 回本 (身份,task) tab 再执行（实测跨进程 switch 有效）。flock（按身份 `/tmp/browser-${SESSION}.lock`）串行防并发 race。
- **tab0 认领去竞态**：每身份一个 marker `/tmp/browser-tab0-${SESSION}.claimed`，flock 临界区内判定——不存在 → 占 tab0 + 建 marker；存在 → `window.open` 新 tab。

### LLM 驱动的离散 action loop（接口保留）

`browser_use.go` 工具不变。状态变化 action（open/click/input/type/wait）跑完 wrapper 自动 `screenshot` 到 `$OUTPUT_DIR` → vision LLM 立即看到新页面 → 决定下一步。这正是「逐步驱动直到登录」的循环。

### 子命令契约（wrapper 必须支持）

`browser_use.go` 调用：`open / state / click / input / type / wait / eval / extract / get`(source) `/ reset`。
`done.go` 调用：`release-tab`（PreDoneCheck 通过后 best-effort 关本 task tab）。
管理类：`install / init / setup / doctor`（不需 session，透传）；`python / tunnel / close / sessions / cloud / profile / switch / close-tab`（带 `--session`）。

### reset / release-tab 改造

- `reset`：原「kill 手动 chromium daemon」→ 改 `browser-use-cli --session liusha close`（停 browse-use daemon）+ 清所有 `/tmp/browser-tab-*.idx` + 删 `tab0-claimed` marker。下次任意 open 自动重启 daemon + 重新认领 tab0。
- `release-tab`：逻辑不变（switch 到本 task tab → close-tab → 删 idx）；仅把 `--cdp-url` 换成 `--session`。

### 次要 bug：PID 1 僵尸回收

sandbox-server 是容器 PID 1，超时杀进程组后孤儿被 reparent 到 PID 1 但不被 `wait()` 回收，积累僵尸。两个候选修法（择一）：

- **A（推荐，简单）**：`launcher.go` 的 `docker run` 加 `--init`（docker 内置 tini 当 PID 1 转发信号 + 回收僵尸）。零代码、最稳。
- B：sandbox-server 起一个 `signal.Notify(SIGCHLD)` + `syscall.Wait4(-1, WNOHANG)` 收割 goroutine。多写代码，A 已够。

---

## 关键决策记录

### 决策 1：回归原生 `--session`，不保留手动 daemon 加自愈

实测证明手动 daemon 是**结构性劣化**（每次 connect_over_cdp 都恶化、不自愈），加 retry/reset 只是治标。原生 `--session` 实测 6 次零劣化，且更快（复用 0-1s）。v35 砍 CDP capture 后手动 daemon 无任何独有收益。

### 决策 2：保留离散 action，不改 browse-use 自主 agent

用户要的是「hunter LLM 逐步驱动 + 每步截图回灌」，不是把决策权交给 browse-use 内部 LLM。`--session` 已让离散 action 跨调用状态保持，无需引入第二个 LLM（成本/复杂度）。

### 决策 3：session 名 = 身份（cookie jar），tab 区分 agent

一个浏览器的同一 context 只有一份 cookie jar——多 tab 共享同一登录身份。故 **session=身份**：同身份所有 agent 共用一浏览器（tab 隔离页面/并发），多身份（越权/BAC）才按需多起浏览器。`browser_use` 加可选 `identity` 字段暴露给 LLM，缺省共享 `default`，不增日常负担。

### 决策 4：`--init` 修僵尸，不在 sandbox-server 写收割逻辑

项目偏好「用现成的、最小代码」。docker `--init` 是标准解法，一个 flag 解决。

---

## 验收标准

- [ ] 容器内手动跑通：单 task `open→state→click→input` 序列跨调用状态保持，无超时。
- [ ] 多 agent tab 模型：tab0（commander）+ window.open 新 tab（striker）并存，switch 不串。
- [ ] 重跑 `e2e.sh active:xss`：browser_use 调用 duration 正常（首个 ~20s，后续秒级），**不再出现连续 60s timeout 级联**。
- [ ] `reset` 能恢复（close daemon → 下次 open 重启）。
- [ ] 容器内 `ps` 无 `<defunct>` 僵尸堆积（`--init` 生效）。
- [ ] proxy/passive 模式 e2e 零回归。
- [ ] 全仓 grep 无 `--cdp-url` / `9222` / `start_chromium_daemon` 残留引用。

## 执行顺序

1. **重写 wrapper**（`deployments/tool-images/pentools/browser-use`）：删手动 daemon，全部 `--session liusha`，tab0-claimed marker，reset 改 close。
2. **容器内热测**：`docker cp` 新 wrapper 进留存容器 → 跑 open→state→click loop + 双 task tab 场景，迭代到通过。
3. **launcher 加 `--init`**（`internal/sandbox/launcher.go` 的 `docker run` args）。
4. **同步注释**：`browser_use.go` reset action 注释（去掉 auth_inject/--cdp-url 措辞）；`skills/tooling/browser-use/SKILL.md`（架构段 + 失败排查表去掉 daemon/reset 措辞，更新为 --session 语义；顺带清掉 v35 已失效的「流量字典/list_flows」措辞）。
5. **重打镜像** `make build-pentools`。
6. **e2e 验收** active:xss + 一个 passive profile 回归。

## 影响范围

**修改**：
- `deployments/tool-images/pentools/browser-use`（wrapper 重写——主体；session=身份、tab=agent、window.open(url) 直开、per-身份文件）
- `internal/tools/external/browser_use.go`（新增可选 `identity` 字段 + `isSafeIdentity` 校验 + IDENTITY env 注入；其余 action/grounding 不变，向后兼容）
- `internal/sandbox/launcher.go`（`docker run` 加 `--init` 收僵尸）
- `skills/tooling/browser-use/SKILL.md`（架构/排查/cookie 段更新为 session=身份；删 v35 已失效的 list_flows 措辞）

**不改**：
- `vuln/bac` skill（curl-replay 多身份对比逻辑已完整，不涉浏览器）
- `internal/sandbox/server/exec.go`（进程组杀逻辑正确，保留）
- 凭证共享 / 流量收口（v35 已定）

**删除**：
- wrapper 内 `start_chromium_daemon` / `reset_chromium_daemon` / `CDP_PORT` / `CDP_URL` / `CHROMIUM_PIDFILE` / `CHROMIUM_LOG` 及全部 `--cdp-url` 引用
