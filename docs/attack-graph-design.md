# 执行图（思维链 + 成果链）设计

> 设计底稿。立项：2026-06-24。状态：方案已定，待动手。
> 一句话：新增**一个投影器**，从现有表实时拼出「agent 怎么想的（思维链）+ 打下了什么（成果链）」的图。**新表 0、新事件种类 0。**
>
> 背景动机源自对标 CyberStrikeAI（CSAI）v1.6.45 的「事实图谱 / 攻击链」，但**刻意不抄它的做法**（理由见 §2）。

---

## 1. 目标（要什么）

- 按**站（per-host）**看一个目标：
  - **思维链** = agent 怎么想的——想 → 做 → 得，含**走不通的死路**和**切换思路的拐弯**。
  - **成果链** = 找到的漏洞 + 它们怎么组合串联（依赖边）。
- **扫描过程中实时可观测**（边扫边长，不是事后生成）。
- 点漏洞能跳回产生它的那段推理（成果 ↔ 思维 互链）。

> 价值定位：这张图是**决策地图**，不是证据仓库。它回答"agent 怎么判断、走到哪、为什么"，天然是**小**的。

---

## 2. 为什么不抄 CSAI

CSAI 的做法（`internal/attackchain/builder.go` 实读）：

```
用户点开"攻击链" → 读对话轨迹（只取最后一轮任务）→ 压到 token 预算
→ 【单次 LLM 调用】吐出整张图的 JSON → 解析渲染（"一次性，不做任何处理"）
```

三个特征都不要：

| CSAI | 后果 |
|---|---|
| 事后点开才生成 | 不是实时 |
| 整张图是 LLM 复述一遍 | 会漏/会编/会漂移，图 ≠ 真相 |
| 只覆盖最后一轮任务 | 不是端到端 |

**根本问题：CSAI 让 LLM 的复述当了唯一产物，真相丢了。** 我们反过来——真相（忠实记录）永久钉死，图是从真相**实时派生**的可重算视图。

---

## 3. 核心决定：图是投影，不是表

**新图不落任何表，它是一个投影器（read-model），像现有的 `internal/sitemap/projector.go` 那样从源表实时拼。**

### 铁证：本项目自己已经把"图表"试错删除 3 次

| migration | 动作 |
|---|---|
| `0001_init` | 建 `graph_node` + `graph_edge`（通用图表，≈ CSAI 的 fact_edges） |
| `0020_finding_relation_drop_graph` | **删 graph_node/graph_edge**。理由：节点全可从 `finding` / `http_flow` 投影派生、无独占信息，只有 finding↔finding 边有独占价值 → 改 `finding_relation` 单表 |
| `0025_finding_relation_drop` | **删 finding_relation** |
| `0026_finding_relation_rebuild` | **重建 finding_relation** + `write_relation` 工具 |
| `0059_finding_depends_on` | **又删 finding_relation**，改 `finding.DependsOn` 内联数组 |

**结论（本项目用真实代价换来的）：图节点几乎全是可投影的派生数据，不该落表；只有真正独占的数据（漏洞、漏洞间依赖）才存，且并入已有结构。** 任何"再建一张图表"的提议都是在捡回删过 3 次的东西，也是在走 CSAI 的回头路。

> 独立判断：`0020` 删 graph 的理由（不存可派生数据）是**对的**，符合单一真相源原则。`0059` 把 finding_relation 内联进 DependsOn 的理由（"LLM 不调 write_relation 工具"）指控的是**手动工具**而非边本身——但因为边以 DependsOn 形式留存了，净结果合理。**新图所需的边（依赖/时序/分叉）全部可投影，仍然 0 新表。**

---

## 4. 数据来源（全部复用现有表）

| 图里要的 | 复用 |
|---|---|
| 想（reasoning / CoT，**含中间猜测**） | `message`(kind='event') + `llm_call` |
| 做 + 得（工具调用 + 结果） | `tool_invocation`（tool_name/args/output_preview/output_size/duration_ms/error_message/done，按 `created_at` 排 = 时序） |
| 树结构（orchestrator → 子代理） | `agent_task` + `tool_invocation.agent_task_id` |
| 漏洞 | `finding` |
| 成果链边（依赖） | `finding.DependsOn`（内联，0059） |
| 证据（流量 / 完整大块） | `http_flow`；`tool_invocation.output_preview` 已把"看到的预览"与"完整 output"分离 |
| 每个站切片 | `finding.host` / flow.host |
| 长期跨多次运行 | `engagement` / `active_scan` / `passive_session`（已存在） |

> 这些表合起来**本来就是一份执行轨迹存储**（等价于 OTel span：llm_call=想、tool_invocation=做+得、agent_task=运行树、finding=成果），只是从没被当成图来读。新图只是给它加一个"读法"。

---

## 5. 节点与边

### 节点种类（就 3 种 + 1 个边界）
- **想（reasoning）**：一次模型调用，正文 = CoT。**中间猜测（"这站可能是 WP6.2"）就是想的正文，不单设种类。**
- **做+得（action）**：一次工具调用，args = 做、output = 得。（不为"得"单设节点，observation 即 action 的 output。）
- **漏洞（finding）**：引用 `finding` 实体，不复制。
- **agent 边界**：树的分叉点（orchestrator → 子代理）。

> 取消了早期设想的"猜测（hypothesis）"独立种类——它和"想"重复。**因此新增事件种类 0 个。**

### 边
- **树（parent / 时序）**：来自 `agent_task` 树 + 同一 run 内 `created_at` 顺序。
- **依赖（depends_on）**：来自 `finding.DependsOn`，构成成果链。
- **证据（evidence）**：finding → 产生它的 flow / action。
- **死路、拐弯**：**投影时派生**（启发式：一枝走到头且无 finding = 死路；死路后的下一兄弟枝 = 拐弯近似），**不落库**。

---

## 6. 原文 vs 摘要 + 证据分离

- **节点存原文**（agent 实际看到的——即被喂给它的截断预览，**不是它根本没读的几十 MB**）。图上只显示**代码生成的短标签**；点开看原文。
- **图存决策，不存证据**：几十 MB 的原始 dump 留在原处（完整 output 在 llm_call/message，预览在 tool_invocation），**图节点只放指针**。→ 图永远小 → 永久存零压力。
- **LLM 摘要只用于"折叠/里程碑"缩放层**，异步算，**复用已有 compaction**，**绝不替代原文**。

---

## 7. 实时机制（复用现有事件流，不加 LLM）

新图是**现有实时事件流的又一个消费者**——`cmd/scanner/event_sink.go` 已经把每个 agent 事件落 `message` 并 publish 到 Redis → SSE。

```
agent 正常干活（已在发事件，不改它）
  reasoning / tool_call / tool_result / spawn / finding
        │
   event_sink（已存在）── 落 PG（原文，永久）
        └─ Redis ─▶ SSE ─▶ 前端
                            │ 每来一个事件
                            ▼
                      图上长出节点/边   ◀── 实时、边扫边看
```

- 节点 step-开始时出现（status=running，前端脉冲），step-结束时定型（落终态/token）。
- **不给 agent 加"画图工具"**（重蹈 0026 `write_relation`：LLM 会忘、嫌烦、污染工具空间）。图是事件的被动副产物。

---

## 8. LLM 的位置

| 层 | 谁来 | LLM？ |
|---|---|---|
| 真相①：执行轨迹（想/做+得） | 代码，从事件流落库 | ❌ |
| 真相②：漏洞 + 依赖 | 代码 | ❌ |
| 派生：图结构、死路/拐弯 | 投影器（代码 + 启发式） | ❌ |
| 派生：里程碑摘要、叙事 | 异步 | ✅ 可用 |

**铁律：LLM 产物永远是可重算的投影，绝不是真相。** 错了重算一遍，真相一字不动。

---

## 9. 留存

**全部永久**（与"长期慢慢聊的渗透助手"定位、对话永久存一致；图本身只存决策，很小，永久零压力）。

唯一**将来可选**的优化：把特别占地方的**原始大块输出**压缩留摘要——但这是跟图无关的、隔离的、以后的小事，**链路结构与漏洞永不动**。现在不做。

---

## 10. 与 sitemap 的关系

**sitemap 保留，作独立视图**（看**攻击面覆盖**：所有端点，含还没出漏洞的；新图只画动过的地方）。两图并列、互不包含。

**连带改动**：成果链的边（`FindingChain`）现在算在 `sitemap.Projector` 里——**迁移到新图投影器**，sitemap 投影器回归纯端点树。

---

## 11. 不做清单（YAGNI / 守住差异化）

- ❌ 不建 graph_node/graph_edge/fact_edges 等图表（删过 3 次）。
- ❌ 不加"猜测"事件种类（= 想）。
- ❌ 不给 agent 加画图/连边工具。
- ❌ 不做 CSAI 式"事后 LLM 复述成图"。
- ❌ 不把大块原始输出塞进图节点。

---

## 12. 落地动作（最小）

1. **新增一个投影器**（如 `internal/attackgraph/projector.go`）：读 message + tool_invocation + llm_call + agent_task + finding，按 run 树 + 时序 + DependsOn 拼成图；死路/拐弯投影时派生。
2. **迁移**：把 `sitemap.Projector` 里的 FindingChain 逻辑搬过来。
3. **实时**：复用现有 SSE 事件流，前端把同一份流渲染成增量长出的图。
4. **前端**：liusha-ui 加这张图的页面（分层布局，可借 CSAI 的 ELK 式呈现；点节点看原文/跳漏洞）。
5. （可选，后置）派生层：里程碑摘要复用 compaction。

**全程：新表 0、新事件种类 0、agent 不改。**
