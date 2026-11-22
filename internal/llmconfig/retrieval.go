package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RetrievalModel 是检索模型密钥的存储行（单行表 retrieval_model，无行 = 未配置）。
// 向量与重排共用一把密钥，模型与地址固定在 embedding 包（维度绑表结构、门槛按模型标定，
// 都不可换），这里只存密钥——加密与对话 provider 密钥同一套机制（AES-256-GCM，llm.encryption_key）。
type RetrievalModel struct {
	EncryptedAPIKey []byte // AES-256-GCM 密文（nonce||ciphertext）
	APIKeyLast4     string // 明文末 4 位（脱敏辨识用，非密钥值）
	UpdatedAt       time.Time
}

// HasKey 报告是否已设置密钥（与解密判定同口径，只是不解密）。
func (r RetrievalModel) HasKey() bool {
	return len(r.EncryptedAPIKey) > 0
}

// GetRetrievalModel 读检索模型密钥行；未配置（无行）返回零值，不算错误。
func (s *Store) GetRetrievalModel(ctx context.Context) (RetrievalModel, error) {
	var r RetrievalModel
	err := s.pool.QueryRow(ctx, `
		SELECT encrypted_api_key, api_key_last4, updated_at
		FROM retrieval_model
		WHERE id = true`).
		Scan(&r.EncryptedAPIKey, &r.APIKeyLast4, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RetrievalModel{}, nil
		}
		return RetrievalModel{}, fmt.Errorf("get retrieval model: %w", err)
	}
	return r, nil
}

// UpsertRetrievalModel 写入（首次插入）或更换检索模型密钥，返回回读行。
func (s *Store) UpsertRetrievalModel(ctx context.Context, encrypted []byte, last4 string) (RetrievalModel, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO retrieval_model (id, encrypted_api_key, api_key_last4)
		VALUES (true, $1, $2)
		ON CONFLICT (id) DO UPDATE SET encrypted_api_key = EXCLUDED.encrypted_api_key,
		                            api_key_last4 = EXCLUDED.api_key_last4,
		                            updated_at = now()
		RETURNING encrypted_api_key, api_key_last4, updated_at`,
		encrypted, last4)
	var r RetrievalModel
	if err := row.Scan(&r.EncryptedAPIKey, &r.APIKeyLast4, &r.UpdatedAt); err != nil {
		return RetrievalModel{}, fmt.Errorf("upsert retrieval model: %w", err)
	}
	return r, nil
}

// ClearRetrievalModel 删除检索模型密钥行。幂等：未配置时（无行）静默成功——双开页面先后
// 清除时，后到的那次不该收到失败。
func (s *Store) ClearRetrievalModel(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM retrieval_model WHERE id = true"); err != nil {
		return fmt.Errorf("clear retrieval model: %w", err)
	}
	return nil
}
