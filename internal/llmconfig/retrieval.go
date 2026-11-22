package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RetrievalModel 是检索模型的配置行（单行表 retrieval_model，无行 = 全部出厂默认）。
// 可配置：服务地址、向量与重排模型名、一把密钥（向量与重排共用）。
// 三个配置字段空串 = 出厂默认（Jina，见 embedding 包常量），沿用「出厂默认 + 用户层」语义。
// 换模型有两条例外约束，写进测试与提示而非禁止：向量维度须与 corpus.embedding 的
// vector(1024) 一致（保存不拦，实测与运行期校验）；更换向量模型时旧向量全部作废重嵌
// （不同模型的向量不可混在一个空间里）。
type RetrievalModel struct {
	BaseURL         string // 服务地址；空 = 出厂默认
	EmbeddingModel  string // 向量模型名；空 = 出厂默认
	RerankModel     string // 重排模型名；空 = 出厂默认
	EncryptedAPIKey []byte // AES-256-GCM 密文；零长度 = 未设置
	APIKeyLast4     string // 明文末 4 位（脱敏辨识用，非密钥值）
	UpdatedAt       time.Time
}

// HasKey 报告是否已设置密钥（与解密判定同口径，只是不解密）。
func (r RetrievalModel) HasKey() bool {
	return len(r.EncryptedAPIKey) > 0
}

// GetRetrievalModel 读检索模型配置行；未配置（无行）返回零值，不算错误。
func (s *Store) GetRetrievalModel(ctx context.Context) (RetrievalModel, error) {
	var r RetrievalModel
	err := s.pool.QueryRow(ctx, `
		SELECT base_url, embedding_model, rerank_model, encrypted_api_key, api_key_last4, updated_at
		FROM retrieval_model
		WHERE id = true`).
		Scan(&r.BaseURL, &r.EmbeddingModel, &r.RerankModel, &r.EncryptedAPIKey, &r.APIKeyLast4, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RetrievalModel{}, nil
		}
		return RetrievalModel{}, fmt.Errorf("get retrieval model: %w", err)
	}
	return r, nil
}

// RetrievalParams 是检索模型配置的保存入参（密钥语义与 ProviderParams 相同）。
type RetrievalParams struct {
	BaseURL         string
	EmbeddingModel  string
	RerankModel     string
	EncryptedAPIKey []byte // 已加密的密文；nil 且 KeepExistingKey=false 时清空密钥
	APIKeyLast4     string
	KeepExistingKey bool // true：保存时不改动已存密文（编辑表单不重填密钥即保留）
}

// UpsertRetrievalModel 写入（首次插入）或整行更新检索模型配置，返回回读行。
func (s *Store) UpsertRetrievalModel(ctx context.Context, p RetrievalParams) (RetrievalModel, error) {
	keyExpr := "$4"
	last4Expr := "$5"
	if p.KeepExistingKey {
		keyExpr = "COALESCE($4, encrypted_api_key)"
		last4Expr = "COALESCE($5, api_key_last4)"
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO retrieval_model (id, base_url, embedding_model, rerank_model, encrypted_api_key, api_key_last4)
		VALUES (true, $1, $2, $3, `+keyExpr+`, `+last4Expr+`)
		ON CONFLICT (id) DO UPDATE SET base_url = EXCLUDED.base_url,
		                            embedding_model = EXCLUDED.embedding_model,
		                            rerank_model = EXCLUDED.rerank_model,
		                            encrypted_api_key = `+keyExpr+`,
		                            api_key_last4 = `+last4Expr+`,
		                            updated_at = now()
		RETURNING base_url, embedding_model, rerank_model, encrypted_api_key, api_key_last4, updated_at`,
		p.BaseURL, p.EmbeddingModel, p.RerankModel, p.EncryptedAPIKey, p.APIKeyLast4)
	var r RetrievalModel
	if err := row.Scan(&r.BaseURL, &r.EmbeddingModel, &r.RerankModel, &r.EncryptedAPIKey, &r.APIKeyLast4, &r.UpdatedAt); err != nil {
		return RetrievalModel{}, fmt.Errorf("upsert retrieval model: %w", err)
	}
	return r, nil
}

// ClearRetrievalKey 清除检索模型密钥（幂等），保留地址与模型名的自定义——密钥缺失即字面
// 检索，用户不必为「临时停用语义检索」丢掉填好的配置。行不存在（本就未配置）时静默成功。
func (s *Store) ClearRetrievalKey(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE retrieval_model
		SET encrypted_api_key = '', api_key_last4 = '', updated_at = now()
		WHERE id = true AND encrypted_api_key <> ''`); err != nil {
		return fmt.Errorf("clear retrieval key: %w", err)
	}
	return nil
}
