package main

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lookupEnv 薄包装（便于测试替身）。
func lookupEnv(key string) string { return os.Getenv(key) }

// newPgxPoolForE2E 建立 e2e 触发器的直查连接池（活性探测用）。
// e2e 触发器与 DB 同机是 dev 契约；池生命周期由调用方 Close。
func newPgxPoolForE2E(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 2 // 触发器侧只需要偶发探测
	return pgxpool.NewWithConfig(ctx, cfg)
}
