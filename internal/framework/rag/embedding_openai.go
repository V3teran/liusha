package rag

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// OpenAIEmbedding OpenAI Embedding 实现
type OpenAIEmbedding struct {
	client    *openai.Client
	model     string
	dimension int
}

// NewOpenAIEmbedding 创建 OpenAI Embedding
func NewOpenAIEmbedding(apiKey string, model string) *OpenAIEmbedding {
	client := openai.NewClient(apiKey)

	// 根据模型确定维度
	dimension := 1536 // text-embedding-ada-002 默认维度
	if model == "text-embedding-3-small" {
		dimension = 1536
	} else if model == "text-embedding-3-large" {
		dimension = 3072
	}

	return &OpenAIEmbedding{
		client:    client,
		model:     model,
		dimension: dimension,
	}
}

// EmbedText 将文本转换为向量
func (e *OpenAIEmbedding) EmbedText(ctx context.Context, text string) ([]float64, error) {
	req := openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.EmbeddingModel(e.model),
	}

	resp, err := e.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	// 转换为 []float64
	embedding := make([]float64, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float64(v)
	}

	return embedding, nil
}

// EmbedTexts 批量转换文本为向量
func (e *OpenAIEmbedding) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}

	req := openai.EmbeddingRequest{
		Input: texts,
		Model: openai.EmbeddingModel(e.model),
	}

	resp, err := e.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding failed: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: expected %d, got %d", len(texts), len(resp.Data))
	}

	// 转换为 [][]float64
	embeddings := make([][]float64, len(resp.Data))
	for i, data := range resp.Data {
		embedding := make([]float64, len(data.Embedding))
		for j, v := range data.Embedding {
			embedding[j] = float64(v)
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// Dimension 返回向量维度
func (e *OpenAIEmbedding) Dimension() int {
	return e.dimension
}
