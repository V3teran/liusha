package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql 驱动 "pgx"：迁移驱动经 *sql.DB 接入
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/db/migrations"
)

// Migrator 是绑定到一个数据库的迁移器。版本记录在 schema_migrations 表（version + dirty），
// 与 golang-migrate 命令行工具同一张表、同一格式：由命令行工具迁移过的存量库可直接接续。
type Migrator struct {
	m  *migrate.Migrate
	db *sql.DB
}

// NewMigrator 打开 dsn 指向的库并装载内置迁移。用完调 Close。
func NewMigrator(dsn string) (*Migrator, error) {
	if dsn == "" {
		return nil, errors.New("postgres.dsn 未配置（环境变量 LIUSHA_POSTGRES_DSN）")
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("装载内置迁移: %w", err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		_ = src.Close()
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	drv, err := migratepgx.WithInstance(conn, &migratepgx.Config{})
	if err != nil {
		_ = conn.Close()
		_ = src.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		_ = conn.Close()
		_ = src.Close()
		return nil, fmt.Errorf("初始化迁移器: %w", err)
	}
	return &Migrator{m: m, db: conn}, nil
}

// Up 迁移到最新版本，返回迁移前后的版本（0 = 空库）。已是最新时 before == after。
//
// ctx 取消时在当前迁移文件执行完后停下（单个文件不会被打断到一半）。
// 上次迁移中途失败（dirty）时拒绝执行：库结构处于未知的半成品状态，必须人工核对。
func (mg *Migrator) Up(ctx context.Context) (before, after uint, err error) {
	before, err = mg.Version()
	if err != nil {
		return 0, 0, err
	}
	stop := context.AfterFunc(ctx, func() { mg.m.GracefulStop <- true })
	defer stop()
	if err := mg.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return before, 0, describeMigrateErr(err)
	}
	after, err = mg.Version()
	return before, after, err
}

// To 迁移到指定版本（向上或向下）。供迁移测试先建旧结构、写老数据再执行被测迁移。
func (mg *Migrator) To(version uint) error {
	if err := mg.m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return describeMigrateErr(err)
	}
	return nil
}

// Version 返回当前版本（0 = 空库）；dirty 时报错。
func (mg *Migrator) Version() (uint, error) {
	v, dirty, err := mg.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取迁移版本: %w", err)
	}
	if dirty {
		return v, dirtyErr(v)
	}
	return v, nil
}

// Close 释放连接。
func (mg *Migrator) Close() error {
	srcErr, dbErr := mg.m.Close()
	return errors.Join(srcErr, dbErr)
}

// Migrate 把 dsn 指向的库迁移到最新版本，并记录版本变化。
func Migrate(ctx context.Context, dsn string, logger zerolog.Logger) error {
	mg, err := NewMigrator(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = mg.Close() }()
	before, after, err := mg.Up(ctx)
	if err != nil {
		return err
	}
	if before == after {
		logger.Info().Uint("version", after).Msg("数据库已是最新版本")
	} else {
		logger.Info().Uint("from", before).Uint("to", after).Msg("数据库迁移完成")
	}
	return nil
}

func describeMigrateErr(err error) error {
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		return dirtyErr(uint(dirty.Version)) // #nosec G115 // 迁移版本号为四位正整数
	}
	return fmt.Errorf("执行迁移: %w", err)
}

func dirtyErr(v uint) error {
	return fmt.Errorf("数据库迁移版本 %d 处于未完成（dirty）状态：上次迁移中途失败，库结构可能是半成品。"+
		"请对照 db/migrations 中该版本的 SQL，人工把库修复为该版本执行完成后的结构，"+
		"再执行 UPDATE schema_migrations SET dirty = false 后重试", v)
}
