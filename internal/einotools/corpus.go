package einotools

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/corpus"
)

// CorpusSearcher / CorpusAdder 是窄接口，*corpus.Store 自动满足。
type CorpusSearcher interface {
	SearchHybrid(ctx context.Context, query string, queryVec []float32, tags []string, recallN, topK int, rr corpus.Reranker) ([]corpus.Entry, error)
}

type CorpusAdder interface {
	Add(ctx context.Context, e corpus.Entry) (corpus.Entry, error)
}

// CorpusEmbedder 是 search/write_corpus 依赖的 embedding 能力（embedding.Client 满足）。
// nil 时降级：search 退纯 sparse（queryVec 空），write 只落行不 embed。
type CorpusEmbedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedPassage(ctx context.Context, texts []string) ([][]float32, error)
}

const (
	corpusRecallN = 20 // dense/sparse 各召回条数
	corpusTopK    = 5  // rerank 后返回条数
)

// searchCorpusArgs：query 必填，tags 可选（metadata 过滤，缩小候选集提精度）。
type searchCorpusArgs struct {
	Query string   `json:"query" jsonschema:"required" jsonschema_description:"用一句话描述你当前遇到的场景/技术难题（如『某 CAS SSO 的前端登录加密怎么逆向』），检索跨目标可复用打法"`
	Tags  []string `json:"tags,omitempty" jsonschema_description:"可选：已知的技术/场景标签（如 sso:cas、waf:cloudflare、fastjson），先按标签过滤再语义检索，更准"`
}

// BuildSearchCorpus 造 search_corpus 工具（PULL 检索）。embedder/reranker 可空（降级纯 sparse / 合并序兜底）。
func BuildSearchCorpus(store CorpusSearcher, emb CorpusEmbedder, rr corpus.Reranker) (tool.BaseTool, error) {
	return utils.InferTool(
		"search_corpus",
		"检索「跨目标长期知识库」（corpus）——沉淀的可复用打法、专家经验、历史教训。"+
			"遇到具体场景（如某类 SSO 登录、某框架漏洞、某 WAF 绕过）时主动查，看有没有现成套路可复用或获取灵感。"+
			"\n与其它记忆的边界：关于「本目标现在」的情报看注入的情报黑板(lead)，不查这里；"+
			"这里只装「这类东西怎么打」的跨目标知识。返回按相关性排序的 top 条。",
		func(ctx context.Context, in searchCorpusArgs) (map[string]any, error) {
			if in.Query == "" {
				return nil, errors.New("query 必填")
			}
			// embed query（失败/无 embedder → queryVec 空，SearchHybrid 退纯 sparse，不阻塞）。
			var queryVec []float32
			if emb != nil {
				if v, err := emb.EmbedQuery(ctx, in.Query); err == nil {
					queryVec = v
				}
			}
			entries, err := store.SearchHybrid(ctx, in.Query, queryVec, in.Tags, corpusRecallN, corpusTopK, rr)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(entries))
			for _, e := range entries {
				items = append(items, map[string]any{
					"title":   e.Title,
					"content": e.Content,
					"tags":    e.Tags,
					"source":  e.Source,
				})
			}
			return map[string]any{"count": len(items), "knowledge": items}, nil
		})
}

// writeCorpusArgs：title/content/tags。source_task_id 闭包注入（不进 LM 参数）。
type writeCorpusArgs struct {
	Title   string   `json:"title" jsonschema:"required" jsonschema_description:"一句话主题（如『CAS SSO 前端登录加密逆向套路』）"`
	Content string   `json:"content" jsonschema:"required" jsonschema_description:"知识正文：具体怎么做、关键手法/payload，写清到下次遇到同类场景能直接复用"`
	Tags    []string `json:"tags,omitempty" jsonschema_description:"技术/场景标签（如 sso:cas、jwt、fastjson），便于将来按标签检索"`
}

// BuildWriteCorpus 造 write_corpus 工具（写入判据卡门槛，防永久库噪音）。
// hostContext 仅用于工具描述提示，不入库（corpus 无 host）。source=agent、sourceTaskID 闭包注入。
// emb 可空：无 embedder 时只落行、embedding=NULL（仍可 sparse 检索）。
func BuildWriteCorpus(store CorpusAdder, emb CorpusEmbedder, sourceTaskID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_corpus",
		"往「跨目标长期知识库」(corpus) 沉淀一条可复用知识。"+
			"\n\n【只写满足全部三条的】"+
			"\n① 验证过的：实战确认有效，不是猜测；"+
			"\n② 可复用跨目标的：对『这类目标/技术』通用（如某 SSO 的登录逆向套路，凡用此 SSO 的系统皆可复用），不是本次目标专属细节；"+
			"\n③ 非显然的：通用知识里没有的、或本项目特有的。"+
			"\n\n【禁写，改用别的】本次目标专属情报 → write_lead；坐实的漏洞 → write_finding；通用 OWASP 理论 → 不写。",
		func(ctx context.Context, in writeCorpusArgs) (map[string]any, error) {
			if in.Content == "" {
				return nil, errors.New("content 必填")
			}
			if in.Title == "" {
				return nil, errors.New("title 必填")
			}
			// embed content（失败/无 embedder → embedding 空，Add 落 NULL，仍可 sparse 命中）。
			var vec []float32
			if emb != nil {
				if vs, err := emb.EmbedPassage(ctx, []string{in.Content}); err == nil && len(vs) == 1 {
					vec = vs[0]
				}
			}
			saved, err := store.Add(ctx, corpus.Entry{
				Title:        in.Title,
				Content:      in.Content,
				Tags:         in.Tags,
				Source:       corpus.SourceAgent,
				SourceTaskID: sourceTaskID,
				Embedding:    vec,
			})
			if err != nil {
				return nil, fmt.Errorf("保存 corpus 失败: %w", err)
			}
			return map[string]any{"id": saved.ID}, nil
		})
}
