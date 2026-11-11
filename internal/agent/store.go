package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/overridestore"
)

// PromptArchive 是升级前用户自定义过的整份提示词。提示词拆分为章程与契约后，
// 旧的整份文本无法自动拆开，只能归档原文、提示用户对照后自行决定是否重新应用。
type PromptArchive struct {
	Prompt     string    `json:"prompt"`
	ArchivedAt time.Time `json:"archived_at"`
}

// Store 持久化 agent 的用户层（agent_override）与提示词归档（agent_prompt_archive）。
type Store struct {
	pool      *pgxpool.Pool
	overrides overridestore.Table
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:      pool,
		overrides: overridestore.Table{Pool: pool, Name: "agent_override", Key: "agent_code"},
	}
}

// Overrides 读出全部 agent 的用户层（code → 用户层；未自定义的 agent 不出现）。
func (s *Store) Overrides(ctx context.Context) (map[string]override.Overrides, error) {
	return s.overrides.All(ctx)
}

// UpdateOverrides 在锁行事务内用 fn 计算并写回某 agent 的用户层。
func (s *Store) UpdateOverrides(ctx context.Context, code string, fn func(override.Overrides) (override.Overrides, error)) (override.Overrides, error) {
	return s.overrides.Update(ctx, code, func(_ context.Context, _ pgx.Tx, cur override.Overrides) (override.Overrides, error) {
		return fn(cur)
	})
}

// UpdateOverridesTx 在调用方事务内修改某 agent 的用户层（与其它写入原子落定）。
func (s *Store) UpdateOverridesTx(ctx context.Context, tx pgx.Tx, code string, fn func(override.Overrides) (override.Overrides, error)) (override.Overrides, error) {
	return s.overrides.UpdateTx(ctx, tx, code, func(_ context.Context, _ pgx.Tx, cur override.Overrides) (override.Overrides, error) {
		return fn(cur)
	})
}

// Begin 开启事务（跨 agent 用户层与其它表的原子修改用）。
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) { return s.pool.Begin(ctx) }

// PromptArchives 读出全部提示词归档（code → 归档）。
func (s *Store) PromptArchives(ctx context.Context) (map[string]PromptArchive, error) {
	rows, err := s.pool.Query(ctx, "SELECT agent_code, prompt, archived_at FROM agent_prompt_archive")
	if err != nil {
		return nil, fmt.Errorf("读提示词归档: %w", err)
	}
	defer rows.Close()
	out := map[string]PromptArchive{}
	for rows.Next() {
		var code string
		var a PromptArchive
		if err := rows.Scan(&code, &a.Prompt, &a.ArchivedAt); err != nil {
			return nil, fmt.Errorf("读提示词归档: %w", err)
		}
		out[code] = a
	}
	return out, rows.Err()
}

// DeletePromptArchive 删除某 agent 的提示词归档（用户处理完毕）。不存在时返回 pgx.ErrNoRows。
func (s *Store) DeletePromptArchive(ctx context.Context, code string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM agent_prompt_archive WHERE agent_code=$1", code)
	if err != nil {
		return fmt.Errorf("删除提示词归档 %s: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("提示词归档 %s: %w", code, pgx.ErrNoRows)
	}
	return nil
}
