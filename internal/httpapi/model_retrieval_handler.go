// Package httpapi: 检索模型密钥 handler（前端「模型配置」页「检索模型」分区，/models/retrieval）。
//
// 检索模型（向量 + 重排）的模型名与地址固定在 embedding 包（维度绑表结构、门槛按模型标定，
// 都不可换），可配置的只有一把密钥：前端直填明文，handler 用 cryptx.Cipher 加密后经 llmstore
// 落库（与对话 provider 密钥同一套机制）。未配置时知识库只做字面检索，对话与任务不受影响。
// 保存成功后触发一次向量补齐（即时生效）：无向量的条目立刻补上，不必等 runner 的周期巡检。

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

// RetrievalDiagnoser 对检索模型密钥做实测（一次向量 + 一次重排）。apiKey 非空用表单当前值
// （未保存也能测）；为空读已存密钥（未配置或解不开 → error，无法发起）。上游的任何失败
// 都体现在 Steps 里。api 组件注入闭合 llmstore + cryptx + embedding 的实现；nil 时路由不注册。
type RetrievalDiagnoser interface {
	DiagnoseRetrieval(ctx context.Context, apiKey string) (RetrievalDiagnosis, error)
}

// RetrievalBackfill 在检索模型密钥保存后触发一次向量补齐（异步，不阻塞保存响应；调用方
// 已在密钥落库并失效缓存之后调用，补齐读到的是新密钥）。nil 时不触发（单测）。
type RetrievalBackfill interface {
	KickVectorBackfill()
}

// retrievalBody 是 PUT /models/retrieval 的请求体。
type retrievalBody struct {
	APIKey string `json:"api_key"`
}

// getRetrievalHandler 处理 GET /models/retrieval：是否已设置、末 4 位、两个模型名。
// 模型名只读展示（固定不可换），key_present / key_last4 绝不透出密钥值本身。
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

// saveRetrievalHandler 处理 PUT /models/retrieval：设置或更换密钥。保存不强制先测试通过
// （与 provider 一致）；成功后触发即时向量补齐。
func saveRetrievalHandler(api ModelAPI, enc KeyEncrypter, backfill RetrievalBackfill) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b retrievalBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		key := strings.TrimSpace(b.APIKey)
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请填入 API Key（清除密钥用删除）"})
			return
		}
		sealed, err := enc.Encrypt(key)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, fmt.Errorf("加密密钥失败: %w", err))
			return
		}
		row, err := api.SetRetrievalModel(c.Request.Context(), sealed, keyLast4(key))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if backfill != nil {
			backfill.KickVectorBackfill()
		}
		c.JSON(http.StatusOK, gin.H{"retrieval": retrievalJSON(row)})
	}
}

// clearRetrievalHandler 处理 DELETE /models/retrieval：清除密钥（幂等）。已有向量保留，
// 重新配置后接着用；知识库回到字面检索。
func clearRetrievalHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.ClearRetrievalModel(c.Request.Context()); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// diagnoseRetrievalHandler 处理 POST /models/retrieval/diagnose。api_key 非空测表单当前值
// （不落库）；为空测已存密钥——两者皆无时 400（无法发起）。
func diagnoseRetrievalHandler(api ModelAPI, d RetrievalDiagnoser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b retrievalBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if strings.TrimSpace(b.APIKey) == "" {
			row, err := api.RetrievalModel(c.Request.Context())
			if err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
			if !row.HasKey() {
				c.JSON(http.StatusBadRequest, gin.H{"error": "检索模型密钥未配置，请先填写或保存"})
				return
			}
		}
		res, err := d.DiagnoseRetrieval(c.Request.Context(), strings.TrimSpace(b.APIKey))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// retrievalJSON 是检索模型密钥响应的单一序列化点。
func retrievalJSON(r llmconfig.RetrievalModel) gin.H {
	var updatedAt any
	if r.HasKey() {
		updatedAt = r.UpdatedAt
	}
	return gin.H{
		"key_present":     r.HasKey(),
		"key_last4":       r.APIKeyLast4,
		"embedding_model": embedding.EmbeddingModelName,
		"rerank_model":    embedding.RerankModelName,
		"updated_at":      updatedAt,
	}
}
