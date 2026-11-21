// Package embedding 是知识库语义检索的模型服务客户端（Jina 的向量与重排接口）。
//
// 密钥是配置 embedding.api_key（经 LIUSHA_EMBEDDING_API_KEY 注入，配置文件里留空），同一个
// 密钥同时启用向量与重排。密钥缺失时知识库只做字面检索，不阻塞主流程。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/V3teran/liusha/internal/corpus"
)

const (
	// EmbedDim 是 jina-embeddings-v5-text-small 的维度（实测确认），与 corpus.embedding vector(1024) 对齐。
	EmbedDim = 1024

	defaultModel         = "jina-embeddings-v5-text-small"
	defaultRerankModel   = "jina-reranker-v2-base-multilingual"
	embedURL             = "https://api.jina.ai/v1/embeddings"
	rerankURL            = "https://api.jina.ai/v1/rerank"
	httpTimeout          = 30 * time.Second
	taskRetrievalQuery   = "retrieval.query"   // 检索查询的向量
	taskRetrievalPassage = "retrieval.passage" // 入库文本的向量（非对称检索）
)

// ErrNoAPIKey 表示 embedding.api_key 未配置。
var ErrNoAPIKey = errors.New("embedding: embedding.api_key 未配置")

// Models 用密钥构造知识库的语义检索模型；密钥为空返回零值（知识库只做字面检索）。
func Models(apiKey string) corpus.Models {
	c, err := NewClient(apiKey)
	if err != nil {
		return corpus.Models{}
	}
	return corpus.Models{Embedder: c, Reranker: c, EmbeddingModel: c.model, RerankModel: c.rerankModel}
}

// Client 是向量与重排接口的客户端，实现 corpus.Embedder 与 corpus.Reranker。
type Client struct {
	apiKey      string
	model       string
	rerankModel string
	httpc       *http.Client
}

// NewClient 用 apiKey 构造。apiKey 为空返回 ErrNoAPIKey。
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	return &Client{
		apiKey:      apiKey,
		model:       defaultModel,
		rerankModel: defaultRerankModel,
		httpc:       &http.Client{Timeout: httpTimeout},
	}, nil
}

type embedRequest struct {
	Model      string   `json:"model"`
	Task       string   `json:"task"`
	Normalized bool     `json:"normalized"`
	Input      []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// EmbedPassage 编码入库文本。空输入返回空结果（不发请求）。
func (c *Client) EmbedPassage(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return c.embed(ctx, taskRetrievalPassage, texts)
}

// EmbedQuery 编码一条检索查询。
func (c *Client) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.embed(ctx, taskRetrievalQuery, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedding: query 期望 1 个向量，得到 %d", len(vecs))
	}
	return vecs[0], nil
}

func (c *Client) embed(ctx context.Context, task string, texts []string) ([][]float32, error) {
	var resp embedResponse
	if err := c.post(ctx, embedURL, embedRequest{
		Model:      c.model,
		Task:       task,
		Normalized: true, // 归一化 → corpus 用 cosine（vector_cosine_ops）
		Input:      texts,
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embedding: 期望 %d 个向量，得到 %d", len(texts), len(resp.Data))
	}
	out := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		if len(d.Embedding) != EmbedDim {
			return nil, fmt.Errorf("embedding: 维度 %d 与预期 %d 不符", len(d.Embedding), EmbedDim)
		}
		out[i] = d.Embedding
	}
	return out, nil
}

type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n"`
	ReturnDocuments bool     `json:"return_documents"`
}

type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// Rerank 返回按相关度降序的打分结果（下标指向 docs，至多 topN 条）。空文档返回 nil。
func (c *Client) Rerank(ctx context.Context, query string, docs []string, topN int) ([]corpus.Ranked, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if topN <= 0 || topN > len(docs) {
		topN = len(docs)
	}
	var resp rerankResponse
	if err := c.post(ctx, rerankURL, rerankRequest{
		Model:     c.rerankModel,
		Query:     query,
		Documents: docs,
		TopN:      topN,
	}, &resp); err != nil {
		return nil, err
	}
	out := make([]corpus.Ranked, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Index >= 0 && r.Index < len(docs) {
			out = append(out, corpus.Ranked{Index: r.Index, Score: r.RelevanceScore})
		}
	}
	return out, nil
}

// post 发一个 JSON 请求并解析响应到 out。非 2xx 读 body 报错。
func (c *Client) post(ctx context.Context, url string, reqBody, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("jina %s: status %d: %s", url, resp.StatusCode, string(b))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
