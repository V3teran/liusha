# browser-use 每身份进程内 Page 路由服务 设计

**状态**: PoC 通过，实现中
**日期**: 2026-05-30
**作者**: V3teran
**关联**: [2026-05-29-browser-use-native-session-design.md](2026-05-29-browser-use-native-session-design.md)（本设计**取代**其 wrapper + flock 模型）；[2026-05-16-sandbox-server-design.md](2026-05-16-sandbox-server-design.md)（容器执行模型）

---

## 背景

### 上一版（0529）解决了什么、留下了什么

0529 回归 browse-use 原生 `--session`，删掉了手动 chromium daemon + `--cdp-url`，确实修好了「首个调用后 daemon 永久锁死」。模型定为：

- **session = 身份（cookie jar）**：同身份 planner/exploitation 共用一个 `--session` 浏览器。
- **tab = agent**：谁先 `open` 占 tab0，后来者 `window.open(url)` 新 tab；tab 号记 `/tmp/browser-tab-${SESSION}-${TASK_ID}.idx`。
- **flock 串行**：每个需要 tab 上下文的子命令，先 `exec 9>"$LOCK"; flock -x 9`，临界区内 `switch $TAB_IDX` + 跑命令。

身份/tab 模型是对的，本设计**保留**。问题出在最后一条——**flock**。

### 问题现象：同身份并发被 flock 锁成串行

`browser-use-cli` 的 daemon 只有**一个全局"当前活动 tab"**，所有操作作用在它上面，要操作 tab N 必须先 `switch tab N`。同身份多个 agent 并发时，「A switch tab1 → B switch tab2 → A click」会点到 tab2（串台）。wrapper 用 flock 把「switch + 操作」锁成原子来防串台。

代价：**flock 握着跑完整条 bu 命令，包括最慢的 page-settle**。于是同身份下 N 个 agent 的 browser 操作被锁成完全串行——单线程 warm open <1s，但 planner + 3-5 exploitation 真并发时，抢不到锁的 agent 干等，墙钟随并发度线性恶化，且抢不到锁的 exploitation 不去干别的活、纯阻塞空等。

### 这次复盘的实测佐证（本会话）

重放上一次过了的 `active:xss`（PASS=3 findings），逐条 LLM 交互 + tool_invocation 复盘发现：**PASS 不是干净的 Choice A**——planner 预热 open 被 LLM 传的 `timeout_seconds=30` 在 30s 砍掉，jar 没种上；exploitations 各自在登录页重登 / curl 旁路；同身份 flock 争用让 browser 操作排队；伴随每 exploitation 重登的凭证风暴。结论：能力可用但**机制脏**，PASS 靠 exploitation 蛮力 + 一点运气。

### 根因

**flock 不是 chromium 需要的，是 CLI「全局活动 tab」这层抽象逼出来的。** CLI 是无状态进程，每次连 daemon，daemon 持有全局可变的「活动 tab」状态 → 必须 `switch` + 操作 → 必须全局锁。底层 chromium/CDP 本身完全支持多 tab 并发独立操作，不需要这个锁。

### 关键验证：browse-use 0.12.6 actor 层是按 target(tab) 寻址的（本会话同容器实测）

读 `dist-packages/browser_use/actor/page.py`（0.12.6）：

```python
class Page:                                    # actor/page.py
    def __init__(self, browser_session, target_id, session_id=None, llm=None):
        self._target_id = target_id            # 每个 Page 绑死一个 CDP target = 一个 tab
        self._client = browser_session.cdp_client
```

每个操作经 `_ensure_session()` → `attachToTarget(targetId=self._target_id)` 拿**本 tab 的 session_id**，再用它发 CDP：`mouse`(本 tab 点击) / `evaluate` / `goto` / `screenshot` / `reload` / `press` / `get_elements_by_css_selector` / `extract_content` 全绑本 tab。最关键的 `state`：

```python
dom_service.get_dom_tree(target_id=self._target_id, ...)   # target_id 是入参
DOMTreeSerializer(...).serialize_accessible_elements()
serialized.llm_representation()                            # 就是 CLI state 那张 [1][2][3] 清单
```

即 **numbered DOM 索引按 target_id 显式构建，不碰任何全局活动 tab**。全局 `switch` + flock 纯属 CLI 无状态进程 + daemon 传输模型的产物。actor 层每个 action 都有一个绑定到具体 tab、彼此独立的方法。

---

## 目标

- **删掉全局 flock + `/tmp/*.idx` + tab0-claimed marker 这套 sh/文件簿记**，换成一个**每身份进程内 Page 路由服务**：在内存里持有 `BrowserSession` + `pages[task_id] → actor.Page`，操作直打本 tab 的 CDP session。同身份多 agent **真并发**，互不串台、互不阻塞。
- **100% 复用 browse-use** —— DomService、序列化器、numbered 索引、extract、mouse 全沿用，**0 行重造**。只是不再走 `browser-use-cli` 的 daemon 门面，改直接调 `actor.Page` API。
- **保留 0529 的身份/tab 模型**：session=身份(cookie jar)、tab=agent、同身份共浏览器 tab 隔离、多身份多 chromium。
- **保留 `browser_use.go` 工具接口零变化**：action 枚举 / 参数 schema / grounding 坐标换算 / 自动截图全不动；上层 agent LLM 逐步驱动不变。
- **登录态原生共享**：同身份同 context，任一 tab 登录后全身份 tab 共享全套(cookie + localStorage + token)，**无导出导入、无 redis 注入**。

## 非目标

- 不全迁 playwright（browse-use 一行不动，playwright/CDP 本就在底下）。
- 不改 browse-use 自主 agent（LLM driver 仍是我们的 agent LLM，离散 action 不变）。
- 不做 cookie/jar 跨浏览器克隆（凭证不止 cookie、位置不固定，导不准——干净版靠"同 context 原生共享"绕过，根本不导）。
- 不动 `vuln/bac` skill 的 curl-replay 多身份对比逻辑（不涉浏览器）。
- 不恢复 CDP capture（v35 已收口）。

---

## 修复设计

### 整体链路

```
不变:  browser_use.go (Go typed 工具)  ──RunCommand──>  sandbox-server exec
                                                              │ 注入 IDENTITY + HUNTER_ID env
                                                              ▼
                                              browser-use (wrapper, 改为薄客户端)
                                                              │ unix socket /tmp/browser-svc-${IDENTITY}.sock
                                                              ▼
新增:                              per-identity Page 路由服务 (python, 常驻)
                                     ├ BrowserSession  (1 chromium = 1 cookie jar)
                                     └ pages: {task_id -> actor.Page(target_id)}
                                                              ▼
                                                          chromium
```

Go 侧（`browser_use.go`）发的命令形状不变：`IDENTITY=x browser-use <sub> [args]`（wrapper 另从 env 读 `HUNTER_ID`）。改的是 wrapper 内部——从「调 `browser-use-cli --session`」变成「把 `<sub> [args] + HUNTER_ID` 转发给本身份的常驻服务」。

### 每身份服务（替代 CLI daemon + flock）

- **一身份一进程**：owns 一个 `BrowserSession`（一个 chromium、一个 cookie jar、一个 `cdp_client`）。
- **懒启动 + 幂等守卫**：某身份第一个命令到达时，若服务未起则启动它 + 让 browse-use 自启 chromium（冷启 ~20s 只付一次）。守卫只锁这一次启动，**登完即放**——这是全系统唯一保留的锁，且只在「启动」期短暂持有，不在每次操作上。
- **Page 注册表**：`pages: dict[task_id -> Page]`。某 (身份,task) 第一个 `open` → 建新 target(tab) + 注册其 `Page`；后续命令按 `task_id` 查表复用同一 `Page`。这张内存 dict **取代** `/tmp/browser-tab-*.idx` + `tab0-claimed` marker + flock 的全部簿记。
- **谁先开 role-agnostic**：planner 或 exploitation，谁的命令先到就建 tab、注册 Page；与角色无关，天然「丝滑」。
- **无全局锁的并发**：服务跑一个 asyncio loop，每个请求一个 task，操作 `await` CDP；`cdp_client` 单 websocket 按 message-id + session_id 多路复用（cdp_use 负责），**不同 tab 的操作天然交错并发**。同一 tab 不会被并发请求命中（task=单 agent），故无需 per-page 锁。

### action → actor.Page 映射（全部复用 browse-use，0 重造）

| action | 映射 |
|---|---|
| `open` | 新 (身份,task)：经事件总线 `event_bus.dispatch(NavigateToUrlEvent(url, new_tab=True))` 建 target，再 `get_pages()` 取绑定 `Page` 注册（**见 PoC 发现②：建 tab 前当前焦点必须先离开 about:blank**）；已存在：`page.goto(url)`（actor.goto 按 target 寻址，不抢全局焦点） |
| `state` | `page.dom_service.get_dom_tree(target_id=page._target_id)` → `DOMTreeSerializer.serialize_accessible_elements()` → `llm_representation()`；**缓存本次 index→backend_node_id 映射到该 Page** 供 click-by-index |
| `click` index=N | 从该 Page 缓存的 index 映射解析 N → `Element` → `page.mouse` 点击 |
| `click` x/y | grounding 换算后 `page.mouse` 坐标点击（换算仍在 Go 侧 `browser_use.go`，不变） |
| `input` index=N | 解析 N → 元素 focus + type（沿用 actor 一步语义） |
| `input` x/y | click 拿焦点 + type |
| `wait` selector/text | actor 等元素/文本（带 timeout） |
| `eval` | 裸 `Runtime.evaluate({expression:code, returnByValue:True})`（与 CLI `_execute_js` 同路径，不走 actor.Page.evaluate）——保住 Go 契约「最后一个表达式 = 返回值」，**Go 侧不变**（见 PoC 发现①） |
| `source` | `page.content` / get html |
| `extract` | `page.extract_content(prompt, ...)`（browse-use LLM 抽取） |
| 自动截图 | 状态变化 action 跑完 `page.screenshot` → `$OUTPUT_DIR/auto_<ns>.png`（喂回 vision LLM，行为不变） |
| `reset` | 停本身份服务进程 + 杀本身份 chromium；下次任意命令重启 |
| `release-tab` | 关本 (身份,task) 的 target + 从 `pages` 删条目（done.go PreDoneCheck 后调用，语义不变） |

`click index=N` 的 N 是序列化那一刻的临时编号——干净版对**同一个 Page** 做 `get_dom_tree → serialize → llm_representation`，index 语义与现 CLI 完全一致，`state→click` 契约平移不变。

### 多身份（越权/BAC）

不同 `IDENTITY` → 不同 socket → 不同服务进程 → 独立 `BrowserSession` = 独立 chromium。身份数 = 同时在用账号数（常态 1，BAC 3-4），**不是** exploitation 数。3-4 chromium 可持续；每 exploitation 一浏览器（10+）的方案被本设计否决。

### 生命周期 / 清理

- 服务进程随 sandbox 容器存活；容器销毁全死。
- `reset`：停本身份服务 + 杀本身份 chromium（副作用：本身份所有 tab 含登录态丢失，需重 open+登录）。不影响其它身份。
- 0529 引入的 `--init` 收僵尸仍需要（服务/子进程被超时杀进程组后的孤儿回收），保留。

---

## 关键决策记录

### 决策 1：换传输层，不换 browse-use；不全迁 playwright

逼出锁的是 CLI 的全局活动 tab，不是 browse-use。actor 层实测按 target 寻址，DOM 索引/extract/mouse 全在库里、全可复用。砍掉的只有 `browser-use-cli` 的 session daemon 门面 + flock wrapper。**没有平行实现 = 不冗余**。全迁 playwright 反而要重造 numbered 索引（browse-use 招牌价值），是负债。

### 决策 2：内存 Page 注册表取代 flock + /tmp 文件簿记

flock 是 contention 根。一旦操作按 target 直达，全局锁失去存在理由。`pages[task_id]→Page` 内存 dict 同时替掉 `*.idx` + `tab0-claimed` + flock 三套文件/锁机制——更少状态、更少竞态、更少代码。

### 决策 3：保留 `browser_use.go` 接口，wrapper 退化为薄客户端

Go 侧 action/schema/grounding/自动截图全不动，向后兼容，agent prompt 无需改。wrapper 从「shell 编排 flock+switch」退化成「把命令 + HUNTER_ID 转发到 socket」，逻辑大幅变薄。

### 决策 4：唯一保留的锁是「每身份启动一次」守卫

不是每操作锁，是每身份生命周期里一次性的「起进程 + 起 chromium」串行守卫，登完即放。与 0529 那个「每操作抢」的 flock 是两码事。

---

## PoC 实测结论（2026-05-30，容器内 browse-use 0.12.6 跑通，已落定）

PoC 用 `data:` URL（probe3，无外网纯净对照）+ e2e 靶机真实 origin（probe4）双线验证，核心论点全绿。原「待落地核实」四问的结论：

- **✅ 冷启耗时**：扩展缓存后 `BrowserSession.start()` ~2.7s；仅首次冷启慢（一次性下 3 个扩展：uBlock Origin Lite / "I still don't care about cookies" / "Force Background Tab"）。服务每身份只暖一次，计入「首命令」预算，后续秒级。
- **✅ 建新 tab 的 actor API**：走事件总线 `event_bus.dispatch(NavigateToUrlEvent(url, new_tab=True))` 建 target，再 `get_pages()` 取绑定 `Page` 注册。替掉 wrapper 的 `window.open` + `switch 99999` 探测。
- **✅ `cdp_client` 并发发送安全**：单 root（flatten 多路复用）websocket 是稳定单对象；probe3 两 tab 并发 `get_dom_tree` + 5 轮并发 `eval`，串台 0 次、`cdp_client` id 不变、无重连，**无需任何额外串行/per-CDP-send 锁**。
- **✅ 跨 tab 登录态共享**：同 `BrowserSession` = 同 browser context，cookie + localStorage 全共享（probe4：p0 写 `auth=TK0`/`sid=S0`，p1 直接读到）。坐实「登录一次、全 tab 共享、零导入」。

### 实现期须遵守的 3 条 PoC 硬约束

- **发现①（箭头函数约束只针对 actor.Page.evaluate，eval action 不受影响）**：`actor.Page.evaluate` 只接受 `(...args) => ...` 形态，裸 JS 语句报 `ValueError: JavaScript code must start with (...args) => format`。**但 `eval` action 服务侧刻意不走 actor.Page.evaluate**，而是直接发裸 `Runtime.evaluate({expression, returnByValue:True})`（与 CLI `_execute_js` 同路径、无 `awaitPromise`），故箭头函数约束不适用、**Go 侧零改动**——保住「最后一个表达式 = 返回值」契约。
- **发现②（建 tab 前须离开 about:blank）**：`NavigateToUrlEvent(new_tab=True)` 命中 reuse-blank 守卫会复用当前空白 tab（tab 数 =1）。建第二个 tab 前，当前焦点 tab 必须已离开 `about:blank`；且**离开必须走事件总线** `NavigateToUrlEvent(new_tab=False)`——actor.goto 不更新 `agent_focus_target_id`，单用它仍会被复用。建好后的逐 tab 导航可用 actor.goto（按 target 寻址，不抢全局焦点）。
- **发现③（重连作废旧 Page 句柄）**：actor.Page 在构造时快照 `cdp_client` + `_session_id`；一旦重连换掉 `_cdp_client_root` 并清空 sessions，旧句柄就挂死。重连由外网慢导航（readiness watchdog 掉 ws）触发，属良性，`data:`/轻页面不触发。服务须在 `is_reconnecting` 落定后用 `get_pages()` 重取句柄。另：每次导航固定有一次 "Page readiness timeout 8s"（良性，真实 origin 与 data: 同有）。

## 待落地核实（实现期解决，不影响结论）

- **extract / get_element_by_prompt 需要 LLM**：`page.extract_content` 要传 `BaseChatModel`。需把我们现有的 LLM 配置注入服务（沿用 agent 的 provider）。确认 browse-use `llm` 接口与我们的 client 适配方式。

---

## 验收标准

- [ ] 容器内手动：同身份双 task **并发** `open→state→click→input` 序列，两 tab 互不串台、互不阻塞，无 flock 级排队。
- [ ] 同身份任一 tab 登录后，另一 tab 直接 `open` 受保护页即带登录态（原生共享，无导入）。
- [ ] 多身份：传不同 `identity` 各起独立 chromium，cookie jar 互不污染。
- [ ] `browser_use.go` 接口零改动下重跑 `e2e.sh active:xss`：browser 操作 duration 不随 exploitation 并发恶化（首 ~20s，后续秒级），无连续 timeout 级联。
- [ ] `reset`/`release-tab` 语义正确（reset 重启本身份、不影响其它身份；release-tab 只关本 task tab）。
- [ ] 全仓 grep 无 `flock` / `tab0-claimed` / `*.idx` 簿记残留。
- [ ] proxy/passive 模式 e2e 零回归。

## 执行顺序

1. **PoC（容器内，先证再写）**：留存容器里手写一个最小 python 服务，import browse-use，建 2 个 Page，**并发**对各自 `get_dom_tree(target_id)` + `mouse` 点击，确认不串台、不需全局锁、登录态共享。坐实「待落地核实」4 点。
2. **服务实现**：per-identity python 服务（BrowserSession + pages 注册表 + socket + action 映射 + 自动截图 + reset/release-tab）。
3. **wrapper 改薄**：`deployments/tool-images/pentools/browser-use` 从 flock 编排改为 socket 客户端（转发 `<sub> args` + `HUNTER_ID`）；删 flock / `*.idx` / tab0-claimed。
4. **容器热测**：`docker cp` 进留存容器，跑并发双 task + 多身份场景迭代到通过。
5. **重打镜像** `make build-pentools`。
6. **同步注释/SKILL**：`browser_use.go`（如 reset 注释）、`skills/tooling/browser-use/SKILL.md`（架构段 flock/switch 措辞 → Page 路由服务语义）。
7. **e2e 验收**：active:xss + 一个 passive profile 回归。

## 影响范围

**新增**：
- per-identity Page 路由服务（python，容器内 `deployments/tool-images/pentools/` 下）

**修改**：
- `deployments/tool-images/pentools/browser-use`（wrapper 改薄为 socket 客户端；删 flock/idx/marker 全部簿记）
- `skills/tooling/browser-use/SKILL.md`（架构/排查段更新为 Page 路由服务）
- `internal/tools/external/browser_use.go`（**仅注释**——接口/action/grounding 不变）

**不改**：
- `browser_use.go` 的 action 枚举 / 参数 schema / grounding 坐标换算（接口零变化，向后兼容）
- `vuln/bac` skill（curl-replay 多身份对比，不涉浏览器）
- `internal/sandbox/server/exec.go`（进程组杀逻辑正确）
- `internal/sandbox/launcher.go` 的 `--init`（0529 已加，保留收僵尸）

**删除**：
- wrapper 内 flock（`exec 9>"$LOCK"; flock -x 9`）、`/tmp/browser-tab-*.idx`、`/tmp/browser-tab0-*.claimed`、`switch` 编排、`window.open` 探测建 tab 逻辑
