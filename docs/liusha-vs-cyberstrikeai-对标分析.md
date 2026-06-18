# liusha vs CyberStrikeAI-v35 全面对标分析

> 分析日期：2026-06-17　|　基于两个本地工作树的真实代码实读（含未提交状态）
> liusha: `/Users/Xlbula/workspace/programs/go/liusha`
> CyberStrikeAI-v35: `/Users/Xlbula/workspace/programs/go/CyberStrikeAI-v35`
>
> 本版相对 2026-06-11 旧版的主要修订：① 对标对象升级为 **v35** 快照；② **eino 版本双方已同为 v0.8.13**（liusha 旧有的"框架版本更新"优势已不存在）；③ liusha 侧 notes 笔记板已退役、工具数实测 15；④ CyberStrikeAI 体量微增（68.7k→69.4k 行）并新增 `app` / `audit` 等装配与合规层。

---

## 0. 总览

| 维度 | liusha | CyberStrikeAI-v35 |
|---|---|---|
| 真实代码量（非测试，排 vendor） | **17,169 行** | **69,383 行**（4.0×） |
| internal 包数 | 35(多为小而内聚的包) | 23(含 handler 27k、database 7k、multiagent 6k 等巨型包) |
| cmd 入口 | 6（api/scanner/proxy/sandbox-server/e2e/vulnapp） | 5（server/mcp-stdio/test-config/test-external-mcp/test-sse-mcp-server） |
| 存储 | **PostgreSQL + Redis**（分布式） | **SQLite**（go-sqlite3，单体 WAL） |
| 任务调度 | **asynq**（Redis 队列，可水平扩展 scanner） | 进程内 task_manager + batch |
| eino 版本 | v0.8.13 | **v0.8.13（已追平）** |
| 内置工具 | run_command（LLM 自由跑 shell）+ browser_use + 13 域工具，共 **15** | **91 个结构化工具 yaml** |
| 子代理 | 3 active（orchestrator/reconnaissance/exploitation）+ 1 passive（traffic-analysis） | **16（完整 ATT&CK 杀伤链 + 3 种编排器人设）** |
| 编排模式 | 1（eino deep swarm） | **3（Deep / Plan-Execute / Supervisor）** |
| 漏洞/技能 skills | 3（dom-xss/bac/browser-use） | **23（含 SQLi/SSRF/反序列化/文件上传/IDOR/云/容器/移动端…）** |
| 角色预设 | 2 scenario（web-pentest/passive-recon） | **13 role yaml** |

### 定位差异（本质）

- **liusha** = **被动流量驱动 + 对话式**的轻量 AI 渗透平台。核心强项是 MITM 流量代理 → 流量字典 → 单 agent 流量分析，以及 SSE 对话式问答。架构分布式、组件小而内聚，处于 greenfield v1 阶段。
- **CyberStrikeAI-v35** = **全功能攻防平台**。主动渗透 + C2 后渗透 + 多 agent 三模式编排 + RAG 知识库 + MCP 生态 + IM 机器人 + webshell/terminal/网络测绘 + 审计合规层。成熟、功能密集，单体部署。

> 一句话：**两者定位高度重叠（AI 驱动渗透），CyberStrikeAI 在"攻防纵深 + 工具生态 + 编排成熟度"上全面领先；liusha 在"被动流量分析 + 对话式交互 + 分布式架构"上保持差异化优势。原先 liusha 的 eino 版本领先已被 v35 追平，技术栈新旧不再是区分点。**

---

## 1. 功能特性对比

| 功能 | liusha | CyberStrikeAI-v35 | 说明 |
|---|:--:|:--:|---|
| 被动 MITM 流量分析 | ✅ | ❌ | **仅 liusha**：proxy 拦截 → 流量字典 → traffic-analysis 单 agent |
| 主动站点扫描 | ✅ | ✅ | 两者都有；CyberStrikeAI 杀伤链纵深更大 |
| 流量字典三件套（list/view/replay_flow） | ✅ | ❌ | **仅 liusha**：基于真实流量重放，session 上下文零丢失 |
| 对话式问答（读黑板答，不扫描） | ✅ | ✅ | liusha 有 intent 分流 + qa；CyberStrikeAI 走对话历史 + 审计 |
| SSE 实时过程事件 | ✅ | ✅ | liusha 用 Redis pub/sub（scanstream）；CyberStrikeAI 进程内事件总线 |
| 浏览器视觉识别（截图喂 VL） | ✅ | ✅ | liusha vision_relay（防 mimo 400 + 元素索引）；CyberStrikeAI vision 独立模块 |
| 凭证共享 | ✅ | ✅ | liusha redis credentials；CyberStrikeAI project 黑板 |
| **C2 后渗透框架** | ❌ | ✅ | **仅 CyberStrikeAI**：beacon/listener/payload/加密会话 |
| **MCP 协议（接外部工具 + 暴露 server）** | ❌ | ✅ | **仅 CyberStrikeAI**：mcp + einomcp + 2 个 mcp-server |
| **RAG 知识库（向量检索）** | ❌ | ✅ | **仅 CyberStrikeAI**：embedding + SQLite 索引 |
| **可视化攻击链（ATT&CK 图谱）** | 部分 | ✅ | liusha 有 finding.depends_on 链（sitemap 投影）；CyberStrikeAI 从对话单次 LLM 生成图 |
| **HITL 人在回路（危险操作审批）** | ❌ | ✅ | **仅 CyberStrikeAI**：hitl_middleware + interrupt/checkpoint |
| **WebShell 管理** | ❌ | ✅ | **仅 CyberStrikeAI**：生成/连接/文件管理/终端 |
| **WebSocket 终端** | ❌ | ✅ | **仅 CyberStrikeAI** |
| **网络空间测绘（FOFA/Shodan/Quake/ZoomEye）** | ❌ | ✅ | **仅 CyberStrikeAI** |
| **IM 机器人（飞书/钉钉/微信）** | ❌ | ✅ | **仅 CyberStrikeAI** |
| **认证/授权/限流** | ❌ | ✅ | **仅 CyberStrikeAI**：security 包 1,909 行 |
| **审计/留存/脱敏** | ❌ | ✅ | **仅 CyberStrikeAI**：v35 新增 audit 包（507 行） |
| **批量任务** | ❌ | ✅ | **仅 CyberStrikeAI**：batch_task_manager |
| **角色预设系统** | 部分 | ✅ | liusha scenarios（2）；CyberStrikeAI roles yaml（13 种） |

---

## 2. 模块/包结构对比

### CyberStrikeAI-v35 独有的重型模块（liusha 完全没有）

| 模块 | 行数 | 作用 |
|---|--:|---|
| `handler` | **27,230** | 巨型业务层（50 个 .go）：webshell、terminal、fofa、hitl、batch_task、notification、role、project、conversation… |
| `database` | 7,328 | SQLite 数据访问层（业务实体 + 迁移 + 查询） |
| `multiagent` | 6,077 | **多 agent 编排引擎**：plan-execute / supervisor / deep 三模式、checkpoint、HITL 中间件、模型重写管线、孤儿工具裁剪、瞬态重试、transcript 摘要 |
| `app` | 4,161 | **应用装配层（v35 新增）**：把 C2 / HITL / vision / 漏洞 / project-fact 工具接线进 server，含 TLS / HTTP 重定向引导 |
| `c2` | 3,789 | **Command & Control 后渗透框架**：HTTP/TCP/WebSocket 多协议 beacon、植入端管理、payload 构建、会话保活、加密通信 |
| `mcp` + `einomcp` | 3,933 | **MCP 协议**：基于官方 go-sdk 接外部 MCP 工具 + 自身暴露为 MCP server |
| `knowledge` | 3,159 | **RAG 知识库**：tiktoken 切分、embedding、向量检索、SQLite 索引、Markdown 按标题切分、检索后处理 |
| `openai` | 2,093 | OpenAI 兼容 ChatModel 客户端层 |
| `security` | 1,909 | 密码认证、session 生命周期、授权中间件、限流、进程隔离（unix/windows） |
| `agent` | 1,292 | 单 agent runner + token 计数 + trace |
| `attackchain` | 1,200 | 从对话轨迹单次 LLM 调用生成攻击链图（节点 + 边） |
| `skillpackage` | 868 | 技能包加载/打包 |
| `robot` | 736 | 飞书/钉钉/企业微信长连接机器人 |
| `vision` | 541 | 独立 VL ChatModel 客户端（图片→文本描述） |
| `agents` | 526 | markdown 子代理（agents/*.md）加载器 + 编排装配 |
| `audit` | 507 | **审计层（v35 新增）**：会话审计、留存策略、脱敏、限流、资源可用性记录 |
| `project` | 460 | 项目黑板、事实记录、scope 管理、统计 |
| `reasoning` | 266 | extended thinking / reasoning_effort 映射（Claude thinking + DeepSeek reasoning） |

### liusha 独有的模块（CyberStrikeAI 没有对应）

| 模块 | 行数 | 作用 |
|---|--:|---|
| `proxy` | 719 | **MITM 流量代理**：拦截 → 过滤责任链 → XADD Redis Stream |
| `ingestor` | 361 | 流量摄入器：Stream 消费 → 入字典 |
| `filter` | 305 | HTTP 流量责任链过滤器（静态构造） |
| `sitemap` | 463 | 攻击面图 + path 模板化（/user/1、/user/2 归一） |
| `passivesession` | 199 | 被动监控会话（1 host = 1 session） |
| `scanstream` | 84 | Redis pub/sub 过程事件实时管道 |
| `intent` | 45 | 便宜 LLM 判定对话意图（扫描 vs 问答） |
| `qa` | 55 | 读黑板 finding，便宜 LLM 答，不触发扫描 |

> **架构哲学差异**：liusha 是"多个小而内聚的包 + 分布式组件"（KISS/高内聚）；CyberStrikeAI 是"少数巨型包 + 单体进程"（功能密集，handler 27k 行 + app/audit 装配合规层是典型单体业务架构）。

---

## 3. 工具集对比

### liusha：13 个域工具 + browser_use + 万能 run_command（共 15）

```
read_findings / write_finding / update_finding   （漏洞黑板）
read_lessons / write_lesson                      （经验沉淀）
read_credentials / write_credential              （凭证共享）
list_flows / view_flow / replay_flow             （流量字典三件套，liusha 独有）
read_tooling_skill / read_vuln_skill             （按需拉 SKILL.md）
browser_use                                       （typed 浏览器自动化，identity=cookie jar）
run_command                                       （LLM 自由跑任意 shell / pentest 工具）
done                                              （收尾终止）
```

> 注：旧版的 `read_notes` / `write_note` 笔记板已**退役**——agent 思路改直接输出到对话（reasoning 事件），跨 task 上下文走对话历史。

**哲学**：工具少而抽象，把"用什么工具"交给 LLM 在 run_command 里自由组合（sqlmap/nuclei/curl…），用 SKILL.md 教方法论；浏览器和流量字典则做成 typed 工具以保真。

### CyberStrikeAI-v35：91 个结构化工具 yaml

覆盖全栈，每个工具有独立 yaml 定义（参数 schema、用法）：

- **信息收集**：amass / subfinder / fierce / dnsenum / gau / waybackurls / paramspider / katana
- **网络扫描**：nmap / masscan / rustscan / fscan / netexec / nbtscan / arp-scan
- **Web**：dirsearch / ffuf / feroxbuster / gobuster / nikto / nuclei / wpscan / dalfox / xsser / sqlmap / jaeles / x8 / arjun / wafw00f / zap / dotdotpwn / api-schema-analyzer
- **网络测绘**：fofa_search / shodan_search / quake_search / zoomeye_search / dnslog
- **二进制/逆向**：ghidra / radare2 / gdb / angr / pwntools / ropgadget / ropper / one-gadget / objdump / strings / checksec / libc-database / pwninit
- **密码爆破**：hydra / john / hashcat / hashpump / responder
- **取证/隐写**：volatility3 / binwalk / foremost / exiftool / steghide / zsteg / xxd
- **云/容器**：prowler / scout-suite / pacu / cloudmapper / kube-bench / kube-hunter / trivy / clair / checkov / terrascan / falco
- **AD/内网**：bloodhound / impacket / smbmap / rpcclient / enum4linux-ng / metasploit / msfvenom
- **工具链**：execute-python-script / install-python-package / exec / query-execution-result

> **核心差异**：liusha 用"run_command + LLM 自主"模式（极简、灵活，但工具发现靠 LLM 记忆 + SKILL 索引）；CyberStrikeAI 用"91 个结构化工具注册表"（每个工具有 schema、可被精确调用、可枚举，但维护成本高）。**各有优劣，非单纯优劣关系。**

---

## 4. Agent 框架与编排

| 项 | liusha | CyberStrikeAI-v35 |
|---|---|---|
| 框架 | eino v0.8.13 | eino v0.8.13 |
| 编排模式 | **Deep swarm 单一模式**（orchestrator + task 派 sub-agent） | **三模式**：Deep / Plan-Execute / Supervisor |
| 子代理数 | 3 active（orchestrator/reconnaissance/exploitation）+ 1 passive（traffic-analysis） | **16**（3 编排器人设 + recon/attack-surface/vuln-triage/penetration/priv-esc/lateral-movement/persistence/impact-exfil/opsec-evasion/cleanup/intel/engagement-planning/reporting…完整杀伤链） |
| passive 路径 | ✅ 单 agent（traffic-analysis） | ❌ |
| 共享 model 并发 | ✅（源码证实安全） | ✅ |
| 压缩中间件 | ✅ CompactionMiddleware | ✅ summarize 系列（transcript/payload） |
| 计费埋点 | ✅ usage_recorder（按 role/owner） | ✅ einoobserve |
| 截图回灌 | ✅ vision_relay（防 mimo 400） | ✅ vision 模块 |
| **HITL 中间件** | ❌ | ✅ hitl_middleware 危险操作审批 |
| **checkpoint 持久化 + 中断/恢复** | ❌（黑板软恢复） | ✅ eino_checkpoint + interrupt |
| **plan-execute 规划** | ❌ | ✅ 显式规划 + 步骤上限 + 宽容解析 |
| **supervisor 监督** | ❌ | ✅ |
| **孤儿工具裁剪** | ❌ | ✅ orphan_tool_pruner_middleware |
| **瞬态重试中间件** | ✅ ModelRetryConfig | ✅ eino_transient_retry |
| **模型重写管线** | 部分（einollm 适配） | ✅ eino_model_rewrite_pipeline |

> CyberStrikeAI 的 multiagent（6,077 行）远比 liusha einoagent（约 1.1k 行）成熟——三种编排范式 + HITL + checkpoint + 完整中间件管线。**两者框架版本已一致（v0.8.13），差距纯在编排功能广度，不在技术债。**

---

## 5. 架构与数据流

| 项 | liusha | CyberStrikeAI-v35 |
|---|---|---|
| 部署形态 | **分布式**（api / scanner / proxy / sandbox-server 多进程） | **单体**（server 单进程 + docker-compose） |
| 存储 | **PostgreSQL（业务）+ Redis（队列/黑板/pub-sub）** | **SQLite（WAL）** |
| 任务队列 | **asynq**（Redis，可水平扩展 scanner） | 进程内 task_manager + batch |
| 实时通道 | Redis pub/sub → SSE | 进程内事件总线 → SSE |
| 沙箱模型 | **per-agent-run docker 容器**（sandbox-server PID 1） | 工具直接执行 + 进程隔离（security 包） |
| 入口 | REST API（gin）+ MITM 代理（8888） | REST API + MCP + 机器人 + WebSocket terminal |
| 多租户/owner | passive_session / active_scan 双轨 | project 维度 |
| 审计合规 | 无专门层 | audit 包（会话审计/留存/脱敏，v35 新增） |

> **liusha 分布式（PG+Redis+asynq）可水平扩展、组件解耦**；**CyberStrikeAI 单体（SQLite）部署简单、开箱即用但水平扩展受限**。这是两种不同的工程取舍。

---

## 6. 部署与工具镜像

| 项 | liusha | CyberStrikeAI-v35 |
|---|---|---|
| 编排 | Makefile + 多镜像（deployments/tool-images/pentools） | docker-compose.yml + 单 Dockerfile |
| 工具镜像 | pentools 镜像（browser-use + sandbox-server + pentest 工具） | install-tools.sh 装 90+ 工具 |
| 浏览器 | browser-svc.py（CDP capture + 元素索引交互） | vision VL |
| MCP server | ❌ | mcp-servers/（pent_claude_agent / reverse_shell） |
| 插件 | ❌ | plugins/（burp-suite） |
| 知识库种子 | ❌ | knowledge_base/（Prompt Injection / SQL Injection 等） |

---

## 7. 【重点】liusha 对标 CyberStrikeAI-v35 还缺什么

> 按"缺失能力 + CyberStrikeAI 怎么实现 + 对 liusha 的价值/优先级"列出。优先级综合 liusha 定位（被动流量驱动 + 对话式）与实现成本。

### 🔴 P0 — 高价值，契合 liusha 定位，建议优先

| 缺失能力 | CyberStrikeAI 实现 | 对 liusha 的价值 |
|---|---|---|
| **更细的杀伤链子代理** | 13 个 ATT&CK 阶段子代理（priv-esc/lateral/persistence/exfil…） | liusha 只有 3 个 active hunter，后渗透阶段空白。liusha 已有 deep swarm 基础设施，扩充 sub-agent 角色 md 即可，**成本低收益高** |
| **HITL 人在回路** | hitl_middleware + interrupt/checkpoint 中断/恢复 | 危险操作（exploit、写 webshell）人工审批是渗透平台的安全刚需。eino 0.8.13 已有 interrupt 机制（双方同版本，可直接接） |
| **更多漏洞 skills** | 23 个（SQLi/SSRF/反序列化/文件上传/IDOR/命令注入/云/容器…） | liusha 只有 3 个 SKILL。纯内容工作，**直接补 SKILL.md 即可**，对挖洞质量提升最直接 |

### 🟡 P1 — 高价值，但需较大工程投入

| 缺失能力 | CyberStrikeAI 实现 | 对 liusha 的价值 |
|---|---|---|
| **RAG 知识库** | knowledge 包：embedding + 向量检索 + SQLite 索引 | liusha 用静态 SKILL.md，无法检索海量漏洞库/历史报告。RAG 让 agent 按需拉相关知识。需引入 embedding provider + 向量存储 |
| **MCP 协议支持** | mcp + einomcp：接外部 MCP 工具 + 暴露 server | 生态互操作性。接入外部 MCP 工具能快速扩展能力，无需自己包装。eino 0.8.13 已有 MCP 组件 |
| **结构化工具注册表** | 91 个工具 yaml | liusha 的 run_command 灵活但工具发现靠 LLM 记忆。结构化注册表让工具可枚举、参数可校验。可渐进式补高频工具 |
| **plan-execute / supervisor 编排** | multiagent 三模式 | 复杂任务的显式规划能提升长链条成功率。liusha 只有 deep 自主模式 |

### 🟢 P2 — 看战略方向决定是否要

| 缺失能力 | CyberStrikeAI 实现 | 对 liusha 的价值 |
|---|---|---|
| **C2 后渗透框架** | c2 包 3,789 行（beacon/listener/payload） | 真后渗透能力。但这是重型子系统，且 liusha 定位偏"发现"而非"驻留"。**战略决策项** |
| **WebShell 管理** | handler 内 webshell 模块 | 拿到 shell 后的管理。与 C2 配套 |
| **网络空间测绘** | FOFA/Shodan/Quake/ZoomEye | 资产发现前置。可作为 run_command 工具补充（成本低） |
| **审计/留存/脱敏** | audit 包（v35 新增） | 合规与可追溯。多租户/企业部署时需要，liusha 当前无专门层 |
| **认证/授权/限流** | security 包 1,909 行 | 生产部署刚需。liusha 当前几乎无 auth，**上线前必补** |
| **IM 机器人** | robot：飞书/钉钉/微信 | 触达渠道。看产品是否要 IM 入口 |
| **WebSocket 终端** | terminal | 交互式调试。锦上添花 |
| **批量任务** | batch_task_manager | 规模化扫描。看是否要批量场景 |

### ⚪ 不建议盲目跟进（liusha 的差异化反而是优势）

- liusha 的 **passive MITM 流量驱动栈**（proxy/ingestor/filter/sitemap）是 CyberStrikeAI **没有**的独特能力 → 应继续强化，是差异化卖点
- liusha 的 **分布式架构（PG+Redis+asynq）** 比 CyberStrikeAI 的单体 SQLite 更适合规模化与组件解耦 → 保持
- liusha 的 **对话式问答（intent 分流 + qa 读黑板）** 交互更轻 → 保持
- liusha 的 **流量字典三件套（list/view/replay_flow）** 让重放保真、session 零丢失 → 是 active 挖洞的独特弹药

> 注意：旧版列举的"eino 版本更新（0.8.13 vs 0.5.4）"差异化点，在 v35 已被追平，**不再是 liusha 的技术优势**。

---

## 8. 结论

1. **CyberStrikeAI-v35 是成熟的全功能攻防平台**，在攻防纵深（C2/后渗透）、工具生态（91 工具 + MCP + RAG）、编排成熟度（3 模式 + 16 子代理 + HITL + checkpoint）、合规（audit）上全面领先，体量约 4 倍于 liusha，且 eino 框架版本已与 liusha 持平。

2. **liusha 不是 CyberStrikeAI 的子集**——它在 passive 流量驱动、对话式交互、分布式架构、流量字典重放上有 CyberStrikeAI 没有的差异化能力。但"框架版本更新"这一项优势已随 v35 消失。

3. **对标补齐的务实路径**（不盲目追体量）：
   - **先补内容**（P0）：扩杀伤链子代理角色 md + 补漏洞 SKILL + 加 HITL 审批 → 低成本、契合现有架构、直接提升挖洞质量与安全性；HITL 因双方 eino 同版本，接 interrupt 的门槛已降低。
   - **再补能力**（P1）：RAG 知识库 + MCP 协议 + 结构化工具注册表 → 中等投入，显著扩展能力边界。
   - **战略选择**（P2）：C2/webshell/auth/审计/机器人 → 按产品方向取舍，auth 与审计在多租户上线前必补。
   - **守住差异化**：passive 流量栈 + 分布式 + 对话式 + 流量字典是 liusha 的护城河，应强化而非放弃。

> 与其追求"成为第二个 CyberStrikeAI"，不如**在用对标补齐攻防纵深与编排成熟度的同时，把 passive 流量驱动 + 对话式这条差异化路线做深**——这是 liusha 最可能形成竞争壁垒的方向。
