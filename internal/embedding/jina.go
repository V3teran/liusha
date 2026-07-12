// Package embedding 封装 Jina 的 embedding + rerank HTTP client，供 corpus 的 hybrid RAG 用。
//
// 见 spec 2026-07-12-lesson-to-corpus-rag.md §6。密钥走 ENV JINA_API_KEY，绝不入 config/git。
// 密钥缺失时构造返回 ErrNoAPIKey，调用方据此降级（corpus 写入只落行不 embed、检索退纯 sparse）——
// 渗透主流程不该被知识库可用性阻塞。
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
)

const (
	// EmbedDim 是 jina-embeddings-v5-text-small 的维度（实测确认），与 corpus.embedding vector(1024) 对齐。
	EmbedDim = 1024

	defaultModel        = "jina-embeddings-v5-text-small"
	defaultRerankModel  = "jina-reranker-v2-base-multilingual"
	embedURL            = "https://api.jina.ai/v1/embeddings"
	rerankURL           = "https://api.jina.ai/v1/rerank"
	httpTimeout         = 30 * time.Second
	taskRetrievalQuery  = "retrieval.query"   // 检索时的 query 向量
	taskRetrievalPassge = "retrieval.passage" // 入库时的 passage 向量（非对称检索，Jina 最佳实践）
)

// ErrNoAPIKey 表示 JINA_API_KEY 未配置——调用方据此降级，不 fail-fast。
var ErrNoAPIKey = errors.New("embedding: JINA_API_KEY 未配置")

// Embedder 把文本编码为向量。窄接口，便于 mock。
type Embedder interface {
	// EmbedPassage 编码入库文本（task=retrieval.passage）。
	EmbedPassage(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery 编码检索 query（task=retrieval.query）。
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Reranker 对候选文档按与 query 的相关性重排，返回按相关性降序的原始下标。
type Reranker interface {
	// Rerank 返回 docs 的下标切片，按相关性从高到低排序，长度 ≤ topK。
	Rerank(ctx context.Context, query string, docs []string, topK int) ([]int, error)
}

// Client 是 Jina 的 Embedder + Reranker 实现。
type Client struct {
	apiKey      string
	model       string
	rerankModel string
	httpc       *http.Client
}

// NewClient 用 apiKey 构造。apiKey 为空返回 ErrNoAPIKey（调用方降级）。
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

// EmbedPassage 编码入库文本。空输入返回空结果（不打网络）。
func (c *Client) EmbedPassage(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return c.embed(ctx, taskRetrievalPassge, texts)
}

// EmbedQuery 编码单条检索 query。
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
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type rerankResponse struct {
	Results []struct {
		Index int `json:"index"`
	} `json:"results"`
}

// Rerank 返回按相关性降序的原始下标（长度 ≤ topK）。空文档返回 nil。
func (c *Client) Rerank(ctx context.Context, query string, docs []string, topK int) ([]int, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if topK <= 0 || topK > len(docs) {
		topK = len(docs)
	}
	var resp rerankResponse
	if err := c.post(ctx, rerankURL, rerankRequest{
		Model:     c.rerankModel,
		Query:     query,
		Documents: docs,
		TopN:      topK,
	}, &resp); err != nil {
		return nil, err
	}
	out := make([]int, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Index >= 0 && r.Index < len(docs) {
			out = append(out, r.Index)
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
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("jina %s: status %d: %s", url, resp.StatusCode, string(b))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
