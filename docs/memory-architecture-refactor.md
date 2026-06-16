# 记忆架构重构 —— 会话流统一 + 长期对话渗透助手

> 规划文档。重构期间参考、重启续接。绞杀者模式：新机制与 notes 并存 → 逐步替换 → 最后移除，每阶段可独立验证可回退。
> 立项：2026-06-13。状态：方案已定，待动手（从阶段0）。

## 产品定位（已定）
liusha = **能长期慢慢聊的渗透助手**，兼顾 active（对话发起）与 passive（流量驱动）两模式。
不抄 CyberStrikeAI；对话/记忆参考 ChatGPT 与 Claude Code 的通用理论。

## 核心问题（为什么重构）
1. **对话历史没喂 agent**：active 多轮追问不连贯——`BuildUserPrompt` 只注入 brief + 黑板浓缩，不读 message 历史。追问"刚才那个漏洞"agent 不知指啥。
2. **notes 与 message 在 active 下冗余**：agent 的观察既写 notes 又作为工具调用流进 message。
3. **24h 一刀切**与"长期慢慢聊"冲突：notes TTL 24h、passive_session/active_scan max_age 24h——聊到一半草稿过期、会话被回收。

## 概念定义（钉死，别再混）
- **owner** = 一次扫描活动 = 一个对话（conversation 1:1 绑 scan_id）。active=active_scan，passive=passive_session。
- **task** = 一次 agent 运行（hunter run）。一个 owner 下多个 task（orchestrator+子代理+每轮追问各一个）。
- **host** = 目标主机。一个 owner 可挂多 host。
- 四种记忆现状：

| 记忆 | 是什么 | 范围 | 存哪 | TTL | 处置 |
|---|---|---|---|---|---|
| 对话历史(message) | 聊天/工作流水 | 单对话 | PG | 永久 | 留，**改为喂 agent** |
| notes | agent 草稿 | 单扫描(owner) | redis | 24h | **退役**（并入 message） |
| lesson | 跨扫描经验 | 按 host | PG | 永久 | **不动**（真·长期记忆） |
| finding | 漏洞成果 | owner+host | PG | 永久 | **不动** |

## 三个决策（已定）
1. **会话生命周期**：区分"记忆永久 vs 资源临时"。对话/finding/lesson 永久存（PG 不删）；算力资源（沙箱、active scan 运行态）按 **idle 释放**。会话状态机 `运行中/闲置/已归档`，不硬过期。替换 24h 一刀切。
2. **passive 语义**：passive 也建会话流，做成 **可插话**——你能随时打开 passive 会话看 agent 分析、插话指导。两模式统一为 conversation，区别仅"谁先开口"。（比 CSAI 的 batch 绑 conversation 更进一步，liusha 差异化）
3. **工作记忆读多少**：**CC 模式**——最近完整 + 旧的压缩，按 token 预算（历史占模型 context ~50-60%）。**复用 liusha 已有的 eino compaction middleware**，不造新轮子。

## 目标架构（统一会话流）
```
会话流水(message, PG, 永久) = 统一的 agent 工作记忆载体，两模式都用
  active：人机对话         passive：无人对话的工作日志（可插话）
  agent 工作记忆 = 读会话流 + eino compaction 压缩控长度
  → notes 退役（其"跨 task 草稿"角色由会话流承担）
lesson(PG, 按host, 永久) = 长期经验   ← 不动
finding(PG, 永久)        = 成果       ← 不动
```

## 分阶段拆解（绞杀者模式）

### 阶段0 — active 读对话历史（最小可用，立即见效）
- 改：`BuildUserPrompt` 额外读最近 N 条对话消息（user/assistant 文字，滤工具噪音）拼进 prompt。
- 不碰：notes/passive/存储全不动，notes 仍并存。
- 产出：active 立刻"能连贯慢慢聊"，解决追问断片痛点。
- 风险：极低、纯增量。**建议先做这个看效果。**

### 阶段1 — 会话流→工作记忆视图 + 压缩
- 改：定义"从 message 提炼工作记忆"（对话消息+关键事件，旧的 compaction 蒸馏）；agent 读此视图替代 prompt 的 notes 段。
- 产出：active 工作记忆来源从 notes 切到"会话流+压缩"（CC 模式）。

### 阶段2 — passive 建会话流 + 两模式统一 + 可插话
- 改：passive_session 建一条 message 流；passive agent 把分析记进去、读会话流当工作记忆；开放 passive 会话的插话入口。
- 产出：active/passive 记忆机制统一，passive 可插话。

### 阶段3 — notes 退役
- 改：两模式都不读 notes 后，移除 write_note/read_notes 工具 + notes store + 清 redis。
- 产出：notes/message 冗余彻底消除。

### 阶段4 — 去 24h / 长期生命周期
- 改：去 notes TTL（随阶段3）、放宽 scan/session 24h、实现"记忆永久 + 资源 idle 释放"的会话状态机 + 归档。
- 产出："长期慢慢聊"真正成立。

## 不动清单（重构不碰）
- **lesson**：跨扫描长期经验，PG 按 host 永久。位置语义都对。
- **finding**：漏洞成果，PG 永久去重。
- eino agent 装配主体、工具集（除 notes 工具最后退役）。

## 验证策略
- 每阶段 build/vet/-race/短测绿 + 起 Docker 栈端到端（active 发 /chat 看追问连贯；passive 看会话流+插话）。
- 阶段间可回退（绞杀者并存期，旧 notes 路径保留到阶段3）。

## 对标小结（佐证方向，非抄袭）
- CSAI batch 后台任务也绑 conversationID → "两模式统一到对话"业界已验证。
- CSAI 无 passive 流量模式 → passive 可插话是 liusha 独有设计。
- CC auto-compact / ChatGPT Memory → "最近完整+旧压缩 / 长期结构化记忆"通用理论，liusha 复用自己的 eino compaction + lesson。
