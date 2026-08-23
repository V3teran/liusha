# lesson → corpus：跨目标知识库（hybrid RAG）重构设计

> 状态：已实施（P1-P3 完成，2026-07-12）。P1+P2 提交 e8a01d20；P3 见后续提交。
>   - P1 地基：迁移 0079、internal/corpus、删 lesson 包、工具层 search/write_corpus、删 PUSH 注入。
>   - P2 检索：internal/embedding（Jina embed+rerank）、hybrid（dense+sparse→rerank）、集成测试。
>   - P3 写入三路：agent 直写 write_corpus（判据）、收尾蒸馏（finalizeTask complete）、专家导入 CLI（cmd/corpus-import）。
>   - P4（prompt 边界细化）：agents/*.md 已加 search/write_corpus 授权 + 边界口诀，随 P1 一并完成。
> 日期：2026-07-12
> 前置：可清库（lesson 存量可丢，纯 DDL 换表）；已完成 lead 去截断+滚动TTL重构（见 architecture-active-passive.md §3）
> 依赖：pgvector 0.8.4（镜像已带）、pg_trgm（可用）、Jina embedding + reranker API

## 0. 背景与动机

现状 `lesson` 表承担两类职责，实测混在一起：

1. **per-host 经验**（`ListByHost`）——"关于某个目标的历史经验"。与新版 `lead(fact)` 职责重叠（都是"关于这个 host 的既成认知"）。
2. **global hint**（`ListGlobalHints`，host=`*`）——"跨 host 通用业务规则/打法"。这半才是真正的**跨目标长期知识**，lead（按 host 键控）结构上无法承担。

问题：
- **职责重叠**：per-host lesson 和 lead(fact) 做同一件事，两套存储两套注入，冗余。
- **检索原始**：global hint 是**无差别全量注入**（`ListGlobalHints` 取 limit 条全塞 prompt），库一大就是噪音 + token 浪费，没有"按当前目标相关性检索"。
- **来源单一**：只有 agent 自己 `write_lesson`。缺"专家经验 / 历史报告"的离线灌入通道。
- **无沉淀纪律**：agent 边干边写，没有"收尾提炼"，长期库容易被过程噪音污染。

## 1. 目标形态

**砍掉 lesson 的 per-host 那半**（并入 lead），**保留并升级 global 那半**为独立的跨目标知识库 `corpus`：

- **corpus = 跨目标、长期、可检索的知识典籍**。装三类来源的知识：agent 自学蒸馏、专家经验、历史沉淀（后两者统一为 `expert`）。
- **检索 = hybrid RAG**：dense 向量（pgvector）+ sparse 关键词（pg_trgm）两路召回 → Jina reranker 精排 → top-k。
- **读 = PULL 工具** `search_corpus(query)`：agent 遇到具体场景主动查（大库、只是有时相关，不 PUSH 全量）。
- **写 = 三条路**：① agent `write_corpus` 工具（判据卡门槛）② 收尾反思蒸馏（complete 时提炼）③ 专家离线导入通道。
- **lead 不变**：仍是按 host 的短期情报黑板（append-only、全量注入、滚动 TTL）。corpus 与 lead 一短一长、一窄一广，职责不重叠。

## 2. corpus vs lead：边界（务必分清）

| 维度 | lead（情报黑板） | corpus（知识典籍） |
|------|------------------|--------------------|
| 时间性 | 短期（滚动 TTL 30 天） | 长期（永久，除非人工清理） |
| 键控轴 | **host**（关于这个目标） | **无 host，按内容检索**（跨目标可复用） |
| 范围 | 单次交战内跨 agent/run | 跨所有目标、所有历史 |
| 存储 | Redis LIST | PostgreSQL + pgvector |
| 读 | 全量注入（PUSH，无工具） | 检索（PULL，`search_corpus`） |
| 例子 | "本 host 的 /admin/backup 疑似可访问" | "某 SSO 的前端 JS 登录加密逆向套路，凡用此 SSO 的系统皆可复用" |

口诀（写进 prompt）：**关于"这个目标现在"→ lead；关于"这类东西怎么打"（跨目标可复用）→ corpus。**

## 3. corpus 表（PostgreSQL + pgvector）

```sql
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE corpus (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 知识本体
    title        text NOT NULL DEFAULT '',       -- 一句话主题（便于人读 + 关键词命中）
    content      text NOT NULL CHECK (content <> ''),  -- 知识正文（打法/经验/规则）
    tags         text[] NOT NULL DEFAULT '{}',    -- 场景/技术标签（如 sso:cas、waf:cloudflare、cve:2023-xxx），metadata 过滤用
    -- 来源
    source       text NOT NULL CHECK (source IN ('agent','expert')),  -- agent 自学蒸馏 / expert 外部导入
    source_task_id uuid,                          -- agent 来源时溯源到哪次 task（expert 导入为 NULL）
    -- 检索
    embedding    vector(1024),                    -- Jina jina-embeddings-v5-text-small，1024 维；NULL=待补 embed
    content_hash text NOT NULL,                   -- SHA-256(content)，去重
    -- 运维
    hit_count    int NOT NULL DEFAULT 0,          -- 被检索命中并采纳次数（用量衰减/软优先级）
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- dense 向量检索：HNSW 索引（近似最近邻，pgvector 同专用库同款算法）。
-- cosine 距离（Jina normalized=true，向量已归一化）。
CREATE INDEX corpus_embedding_hnsw ON corpus USING hnsw (embedding vector_cosine_ops);

-- sparse 关键词检索：pg_trgm GIN 索引（专有名词/代码标识符精确匹配优于分词全文检索）。
CREATE INDEX corpus_content_trgm ON corpus USING gin (content gin_trgm_ops);
CREATE INDEX corpus_title_trgm   ON corpus USING gin (title gin_trgm_ops);

-- tags metadata 过滤（检索前按标签硬过滤缩小候选集）。
CREATE INDEX corpus_tags_gin ON corpus USING gin (tags);

-- 去重：同内容不重复写。
CREATE UNIQUE INDEX corpus_content_hash_uniq ON corpus (content_hash);
```

- **无 host 列**：corpus 是跨目标的，host 归 lead。这是与 lesson 最本质的结构差异。
- **embedding 可空**：写入时先落行（content_hash 去重），embed 可同步或异步补（见 §5）。NULL embedding 不进 dense 召回，仍可被 sparse 命中。
- **tags 是 hybrid 的"第三条腿"**：检索前 metadata 过滤（如"目标识别出用了 Shiro"→ 先过滤 `tags && '{shiro}'`），再在子集里跑 dense+sparse，又快又准。

## 4. 检索：hybrid RAG（dense + sparse → Jina rerank）

`search_corpus(query, tags?)` 的检索链：

```
1. （可选）metadata 过滤：若给了 tags，先 WHERE tags && $tags 缩小候选集
2. 两路并行召回 top-N（如各 20）：
   ├─ dense：embed(query) → ORDER BY embedding <=> query_vec LIMIT N（HNSW）
   └─ sparse：ORDER BY similarity(content, query) DESC LIMIT N（pg_trgm）
3. 合并去重两路候选（按 id）
4. Jina reranker：把 query + 候选 content 列表送 rerank API → 返回相关性重排
5. 取 rerank 后 top-k（如 5）返回 agent
6. 命中采纳的条目 hit_count++（软优先级/衰减用）
```

**为何 Jina rerank 而非 RRF 算法融合**：用户明确要 cross-encoder 精排（比纯排名融合精度高）。代价是每次检索多一次 Jina rerank 网络往返——可接受（检索非高频，agent 遇到场景才查）。rerank 失败降级：跳过 rerank，用 dense 距离排序兜底（不阻塞检索）。

**embedding 归一化**：Jina `normalized=true`，向量已归一化，用 cosine（`vector_cosine_ops`）。

## 5. 写入：三条路

### 5.1 agent 直写 `write_corpus`（判据卡门槛）

工具 `write_corpus(title, content, tags)`。`source=agent`、`source_task_id` 闭包注入。写入判据**写死进工具描述**（高门槛，永久库最怕噪音）：

> 只写满足**全部**条件的知识：
> - **验证过的**：实战确认有效，不是猜测；
> - **可复用跨目标的**：对"这类目标/技术"通用（如某 SSO 的登录逆向套路），不是本次目标专属细节；
> - **非显然的**：LM 通用知识里没有的、或本项目特有的。
>
> 【禁写，改用别的】本次目标专属情报 → write_lead；坐实的漏洞 → write_finding；通用 OWASP 理论 → 不写。

写入流程：算 content_hash → `ON CONFLICT (content_hash) DO UPDATE hit_count++`（去重，重复写视为一次"再次确认"）→ embed（同步或入队异步）。

### 5.2 收尾反思蒸馏（distill）

**触发**：`finalizeTask(complete=true)` 时（失败/中止 task 不触发——没什么可提炼）。用 **light provider**（便宜）。

**取材三份**（均现成）：
- 本 task 的**对话轨迹**（reasoning + 工具调用；走现有对话历史读取路径，可能已压缩，避免几万 token 原样喂）；
- 本 task 的 **finding**（坐实成果）；
- 本 task 所属 host 的 **lead**（尤其 deadend/fact，过程情报）。

**蒸馏 prompt 大意**：看这次交战的轨迹+成果+情报，提炼出**跨目标可复用**的打法/教训（判据同 §5.1），产出 0~N 条 → 每条走 `write_corpus` 落库（source=agent）。明确要求省略一次性目标细节。

**与直写的关系**：两条路并存——agent 干活中遇明确可复用打法可当场直写；收尾蒸馏兜底把散落经验提炼一遍。蒸馏产出同样过 content_hash 去重，不会和直写重复。

### 5.3 专家离线导入通道

专家经验 / 历史报告**不是 agent 写的**，独立 ingestion 入口：
- 一个 CLI（`cmd/corpus-import` 或 admin API），读文档 → 切条 → `source=expert` 落库 → embed。
- 与 agent 的 `write_corpus` 工具**分开**（不同来源、不同信任级、不同触发）。
- 第一版可先做最简 CLI（读 JSON/markdown 批量导入），够专家灌初始知识即可。

## 6. embedding 客户端（Jina）

- **模型**：`jina-embeddings-v5-text-small`，**1024 维**（已实测确认），`normalized=true`。
- **端点**：`https://api.jina.ai/v1/embeddings`；rerank 端点 `https://api.jina.ai/v1/rerank`。
- **密钥**：`JINA_API_KEY` 走 ENV（`.env.local`），**绝不入 config.yaml/git**。缺失时：corpus 写入降级为"只落行、embedding=NULL"（仍可 sparse 检索），检索降级为纯 sparse——不 fail-fast（渗透主流程不该被知识库可用性阻塞）。
- **client 落点**：`internal/embedding`（Jina embed + rerank 两个薄封装），或复用 eino `components/embedding` 抽象。
- **task 参数**：Jina 支持 `retrieval.query`（检索时）/ `retrieval.passage`（入库时）区分——入库用 passage、检索用 query（非对称检索，Jina 最佳实践）。

## 7. 迁移与模块改造（纯 DDL，lesson 存量可丢）

**迁移 0079**（下一个可用编号，当前最新 0078）：
- `CREATE EXTENSION vector / pg_trgm`；建 `corpus` 表 + 索引（§3）。
- `DROP TABLE lesson`（存量可丢）。

**模块改造**：
- 新增 `internal/corpus`（model + store，含 hybrid 检索 SQL）、`internal/embedding`（Jina client）。
- 删 `internal/lesson` 包。
- `internal/einotools/lessons.go` → `corpus.go`：`BuildReadLessons`/`BuildWriteLesson` → `BuildSearchCorpus`（PULL）/`BuildWriteCorpus`。**注意 read 语义变了**：原 `read_lessons` 是无参列全部，新 `search_corpus` 是带 query 的检索。
- `internal/einoagent/{role_tools,traffic_analysis_tools}.go`：`LessonStore` → `CorpusStore`，工具注册名改 `search_corpus`/`write_corpus`。
- `internal/builder/agent/user_prompt.go`：**删掉 lesson 的 PUSH 注入**（`loadKnowledgeForPrompt` 那段）——corpus 改 PULL，不再无差别注入。per-host 经验的注入职责已由 lead 承担。
- `cmd/scanner/*`：`h.lessons` → `h.corpus`；`finalizeTask` 加蒸馏调用（complete 分支）。
- 授权角色：`search_corpus` + `write_corpus` 授 recon/exploitation/traffic-analysis（同 lead）；planner 不授 write（与现状一致）。

## 8. 实施阶段

- **P1 地基**：迁移 0079（corpus 表 + 扩展 + 索引，DROP lesson）；`internal/corpus` model+store（先只 CRUD + sparse 检索，dense 留空）；删 lesson 包 + 改所有引用点编译通过。验证：build + 现有测试绿。
- **P2 embedding + hybrid 检索**：`internal/embedding` Jina client（embed + rerank）；corpus store 补 dense 召回 + hybrid 合并 + rerank；`search_corpus` 工具接线。验证：集成测试（dbtest 起 pgvector，插样本，验 hybrid 召回顺序）。
- **P3 写入三路**：`write_corpus` 工具（判据）；收尾蒸馏（finalizeTask complete 分支，light provider）；专家导入 CLI。验证：蒸馏产出落库、去重生效、expert 导入可检索。
- **P4 prompt 边界**：prompt 写清 corpus/lead/finding 三者边界口诀；删 lesson 的 PUSH 注入。验证：agent 遇场景会主动 search_corpus。

## 9. 评审已定结论（2026-07-12）

1. **蒸馏取材**：用**压缩后**的对话轨迹（走现有对话历史读取路径），控 token；配 finding + lead 一起喂 light provider。
2. **embed 时机**：**同步**——write_corpus 时直接 embed 落库（写入非高频，YAGNI，不引 asynq 异步复杂度）。Jina 不可用时降级只落行、embedding=NULL（§6）。
3. **专家导入**：输入 **markdown**；每条的 **title/tags 由 LLM 自动打标**（导入 CLI 读 markdown → LLM 生成 title + tags → 落库 source=expert → embed）。
4. **hit_count**：**永久保留**，不做定期衰减/淘汰（第一版 YAGNI；hit_count 仅作软优先级信号，检索排序参考，不触发删除）。
5. **rerank 候选集 / top-k**：两路各召回 **N=20**、rerank 后返回 **top-5**。均可配（`corpus_recall_n` / `corpus_top_k`），先定此默认值。

## 10. 设计取舍备忘

- **pgvector 不用独立向量库**：corpus 万级规模，pgvector（HNSW）+ pg_trgm 一条 SQL 做 hybrid，零新增有状态组件、元数据与向量同事务一致。独立向量库是千万级+/超高 QPS 才需要的（over-engineering + 徒增运维债 + sparse 那路还得再接引擎）。
- **PULL 不 PUSH**：corpus 大且偶尔相关 → 检索工具让 agent 按需查；lead 小且总相关 → 全量注入。各得其所（agentic RAG）。
- **Jina rerank 不 RRF**：用户选 cross-encoder 精排，精度优先；失败降级 dense 排序兜底。
- **pg_trgm 不 tsvector**：渗透术语（CVE/框架名/代码标识符）精确匹配优于语义分词。
- **砍 per-host lesson**：与 lead(fact) 重叠，职责并入 lead；corpus 专注跨目标，概念不重叠。
- **写入高门槛 + 收尾蒸馏**：永久库最怕噪音，判据卡直写 + 蒸馏提炼精华，双保险。

