// Package embedding 是知识库语义检索的模型服务客户端（OpenAI 兼容的向量与重排接口）。
//
// 服务地址与两个模型名可由用户在「模型」页填写（空 = 出厂默认 Jina），密钥同页维护
// （AES-256-GCM 加密落库），同一个密钥同时启用向量与重排。密钥缺失时知识库只做字面检索，
// 不阻塞主流程。向量维度在请求响应处校验（须与 corpus.embedding 的 vector(1024) 一致），
// 不一致报错退字面检索——保存不拦维度，实测与运行期给明确提示。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/corpus"
)

const (
	// EmbedDim 是出厂默认向量模型 jina-embeddings-v5-text-small 的维度（实测确认），
	// 与 corpus.embedding vector(1024) 对齐。换向量模型必须同维度（测试时实测校验），
	// 换模型即全量重建向量（不同模型的向量不可混在一个空间）。
	EmbedDim = 1024

	// DefaultBaseURL / EmbeddingModelName / RerankModelName 是出厂默认（界面上只读展示为
	// 占位提示）。字段留空即用它们，沿用「出厂默认 + 用户层」语义。
	DefaultBaseURL     = "https://api.jina.ai"
	EmbeddingModelName = "jina-embeddings-v5-text-small"
	RerankModelName    = "jina-reranker-v2-base-multilingual"

	httpTimeout          = 30 * time.Second
	taskRetrievalQuery   = "retrieval.query"   // 检索查询的向量
	taskRetrievalPassage = "retrieval.passage" // 入库文本的向量（非对称检索）
)

// ErrNoAPIKey 表示检索模型密钥未配置。
var ErrNoAPIKey = errors.New("embedding: 检索模型密钥未配置")

// Config 是检索模型客户端的连接参数；空字段用出厂默认。
type Config struct {
	APIKey         string
	BaseURL        string // 服务地址；空 = DefaultBaseURL
	EmbeddingModel string // 向量模型名；空 = EmbeddingModelName
	RerankModel    string // 重排模型名；空 = RerankModelName
}

// NormalizeBaseURL 把用户填的服务地址归一为「不带版本段与操作路径的基础地址」：
// 去首尾空白与末尾斜杠、去掉误粘的 /embeddings / /rerank 操作路径；版本段 /v1 统一由
// 客户端拼接（基础地址含 /v1 与不含都落成同一结果）。无法解析的地址原样返回，交给
// 调用方的 http(s) 校验。
func NormalizeBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/embeddings", "/rerank"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = base[:len(base)-len("/v1")]
	}
	return base
}

// Client 是向量与重排接口的客户端，实现 corpus.Embedder 与 corpus.Reranker。
type Client struct {
	apiKey      string
	embedURL    string
	rerankURL   string
	model       string
	rerankModel string
	httpc       *http.Client
}

// NewClient 按配置构造。apiKey 为空返回 ErrNoAPIKey。
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	base := NormalizeBaseURL(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	model, rerankModel := cfg.EmbeddingModel, cfg.RerankModel
	if model == "" {
		model = EmbeddingModelName
	}
	if rerankModel == "" {
		rerankModel = RerankModelName
	}
	return &Client{
		apiKey:      cfg.APIKey,
		embedURL:    base + "/v1/embeddings",
		rerankURL:   base + "/v1/rerank",
		model:       model,
		rerankModel: rerankModel,
		httpc:       &http.Client{Timeout: httpTimeout},
	}, nil
}

// Models 用配置构造知识库的语义检索模型；密钥为空返回零值（知识库只做字面检索）。
func Models(cfg Config) corpus.Models {
	c, err := NewClient(cfg)
	if err != nil {
		return corpus.Models{}
	}
	return corpus.Models{Embedder: c, Reranker: c, EmbeddingModel: c.model, RerankModel: c.rerankModel}
}

// EmbedURL / RerankURL 是本客户端实际请求的两个端点（连接诊断展示实际请求地址）。
func (c *Client) EmbedURL() string  { return c.embedURL }
func (c *Client) RerankURL() string { return c.rerankURL }

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
	if err := c.post(ctx, c.embedURL, embedRequest{
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
			return nil, fmt.Errorf("向量维度 %d 与库结构 %d 不符，请换 %d 维的向量模型", len(d.Embedding), EmbedDim, EmbedDim)
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
	if err := c.post(ctx, c.rerankURL, rerankRequest{
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
		return fmt.Errorf("retrieval %s: status %d: %s", url, resp.StatusCode, string(b))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
