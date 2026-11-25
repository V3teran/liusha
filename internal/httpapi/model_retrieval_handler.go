// Package httpapi: 检索模型配置 handler（前端「模型配置」页「检索模型」分区，/models/retrieval）。
//
// 检索模型（向量 + 重排）可配置四样：服务地址、向量模型名、重排模型名、一把密钥——
// 空字段 = 出厂默认（Jina），与对话 provider 的表单同构但只保留有意义的字段（单值鉴权、
// 启用开关等对话专属概念不在此列）。密钥加密落库（cryptx.Cipher，与 provider 密钥同一套
// 机制），前端直填明文仅在本次请求内持有。未配置密钥时知识库只做字面检索，对话与任务不受
// 影响。保存后触发向量收口：更换了向量模型则旧向量全部作废（不同模型的向量不可混用），
// 随即立即补齐，不等周期巡检。

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// 检索模型诊断步骤（按依赖顺序渲染：重排在向量之后——先确认密钥能换向量，再看重排）。
const (
	StepEmbedding = "embedding"
	StepRerank    = "rerank"
)

// RetrievalDiagnosis 是检索模型的一次实测结果：向量与重排各一步。
type RetrievalDiagnosis struct {
	OK    bool            `json:"ok"` // 两步都通过（密钥可用）
	Steps []DiagnosisStep `json:"steps"`
}

// RetrievalProbeSpec 是诊断的入参规格（表单当前值）：空字段按已存值或出厂默认解析。
type RetrievalProbeSpec struct {
	BaseURL        string
	EmbeddingModel string
	RerankModel    string
	APIKey         string // 非空用它（表单当前值）；为空读已存密钥
}

// RetrievalDiagnoser 对检索模型规格做实测（一次向量 + 一次重排）。密钥来源：spec.APIKey
// 非空用它；为空读已存密钥（未配置或解不开 → error，无法发起）。上游的任何失败都体现在
// Steps 里——向量一步会校验维度与库结构一致。api 组件注入闭合 llmstore + cryptx +
// embedding 的实现；nil 时路由不注册。
type RetrievalDiagnoser interface {
	DiagnoseRetrieval(ctx context.Context, spec RetrievalProbeSpec) (RetrievalDiagnosis, error)
}

// RetrievalModelIndex 是检索模型保存后的向量收口（api 组件注入 corpus.Service 适配器）：
// 更换向量模型时旧向量全部作废（ResetVectors），随即立即补齐（KickVectorBackfill，异步
// 不阻塞保存响应；调用时新配置已落库并失效缓存，补齐读到的是新值）。nil 时不收口（单测）。
type RetrievalModelIndex interface {
	ResetVectors(ctx context.Context) (int64, error)
	KickVectorBackfill()
}

// retrievalBody 是 PUT /models/retrieval 的请求体。api_key 留空 = 保留已存密钥（编辑时
// 的默认行为）；配置字段留空 = 出厂默认。
type retrievalBody struct {
	BaseURL        string `json:"base_url"`
	EmbeddingModel string `json:"embedding_model"`
	RerankModel    string `json:"rerank_model"`
	APIKey         string `json:"api_key"`
}

// getRetrievalHandler 处理 GET /models/retrieval：返回生效配置（已存值或出厂默认）与
// 密钥状态。绝不回显明文或密文。
func getRetrievalHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		row, err := api.RetrievalModel(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"retrieval": retrievalJSON(row)})
	}
}

// saveRetrievalHandler 处理 PUT /models/retrieval：保存配置与密钥（保存不强制先测试通过，
// 与 provider 一致）。更换了向量模型时旧向量全部作废并立即补齐。
func saveRetrievalHandler(api ModelAPI, enc KeyEncrypter, index RetrievalModelIndex) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b retrievalBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		prev, err := api.RetrievalModel(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		p := llmconfig.RetrievalParams{
			BaseURL:        strings.TrimSpace(b.BaseURL),
			EmbeddingModel: strings.TrimSpace(b.EmbeddingModel),
			RerankModel:    strings.TrimSpace(b.RerankModel),
		}
		if p.BaseURL != "" {
			if err := llmconfig.ValidateBaseURL(p.BaseURL); err != nil {
				respondErr(c, http.StatusBadRequest, err)
				return
			}
		}
		if b.APIKey != "" {
			sealed, err := enc.Encrypt(b.APIKey)
			if err != nil {
				respondErr(c, http.StatusInternalServerError, fmt.Errorf("加密密钥失败: %w", err))
				return
			}
			p.EncryptedAPIKey = sealed
			p.APIKeyLast4 = keyLast4(b.APIKey)
		} else {
			p.KeepExistingKey = true // 编辑未重填密钥：沿用已存密文
		}
		row, err := api.SetRetrievalModel(c.Request.Context(), p)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		// 向量收口：向量模型变了，旧向量（按旧模型算的）对新模型是错的，全部作废后立即重嵌
		if index != nil && effectiveEmbeddingModel(prev) != effectiveEmbeddingModel(row) {
			if _, err := index.ResetVectors(c.Request.Context()); err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
		}
		if index != nil {
			index.KickVectorBackfill()
		}
		c.JSON(http.StatusOK, gin.H{"retrieval": retrievalJSON(row)})
	}
}

// clearRetrievalKeyHandler 处理 DELETE /models/retrieval/key：只清除密钥（幂等），保留地址与
// 模型名的自定义。知识库回到字面检索；重配密钥即恢复。
func clearRetrievalKeyHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.ClearRetrievalKey(c.Request.Context()); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// diagnoseRetrievalHandler 处理 POST /models/retrieval/diagnose：按表单当前值（空字段按
// 已存值或出厂默认）实测一次向量 + 一次重排。api_key 非空测它（不落库），为空测已存
// 密钥——两者皆无时 400（无法发起）。
func diagnoseRetrievalHandler(api ModelAPI, d RetrievalDiagnoser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b retrievalBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		prev, err := api.RetrievalModel(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if strings.TrimSpace(b.APIKey) == "" && !prev.HasKey() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "检索模型密钥未配置，请先填写或保存"})
			return
		}
		res, err := d.DiagnoseRetrieval(c.Request.Context(), RetrievalProbeSpec{
			BaseURL:        b.BaseURL,
			EmbeddingModel: b.EmbeddingModel,
			RerankModel:    b.RerankModel,
			APIKey:         strings.TrimSpace(b.APIKey),
		})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// effectiveEmbeddingModel 是向量模型名的生效值（空 = 出厂默认）——换模型判定用它，
// 不比较原始填写值（把默认显式填进去不算换）。
func effectiveEmbeddingModel(r llmconfig.RetrievalModel) string {
	if r.EmbeddingModel == "" {
		return embedding.EmbeddingModelName
	}
	return r.EmbeddingModel
}

// retrievalJSON 是检索模型配置响应的单一序列化点：生效配置（空字段落出厂默认）+
// 密钥状态。updated_at 仅在有过保存时给出。
func retrievalJSON(r llmconfig.RetrievalModel) gin.H {
	baseURL := r.BaseURL
	if baseURL == "" {
		baseURL = embedding.DefaultBaseURL
	}
	embedModel, rerankModel := effectiveEmbeddingModel(r), r.RerankModel
	if rerankModel == "" {
		rerankModel = embedding.RerankModelName
	}
	var updatedAt any
	if !r.UpdatedAt.IsZero() {
		updatedAt = r.UpdatedAt
	}
	return gin.H{
		"base_url":        baseURL,
		"embedding_model": embedModel,
		"rerank_model":    rerankModel,
		"key_present":     r.HasKey(),
		"key_last4":       r.APIKeyLast4,
		"updated_at":      updatedAt,
	}
}
