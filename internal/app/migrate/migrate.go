// Package migrate 是 `liusha migrate` 的装配（serve 启动前同样执行）：先把数据库结构迁移到
// 最新版本，再把出厂的技能与场景同步进表（internal/builtinsync：用户没改过的跟随新版，改过的
// 不动）。所有部署形态都先跑它，业务组件起来时表里已有出厂内容。
package migrate

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/scenariostore"
	"github.com/V3teran/liusha/internal/skillstore"
)

// Run 迁移数据库结构并同步出厂内容。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("migrate")
	if err := db.Migrate(ctx, cfg.Postgres.URL, logger); err != nil {
		return err
	}
	pool, err := db.NewPgPool(ctx, cfg.Postgres, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	for _, step := range []struct {
		name string
		sync func(context.Context) ([]builtinsync.Action, error)
	}{
		{"技能", skillstore.New(pool).SyncBuiltins},
		{"场景", scenariostore.New(pool).SyncBuiltins},
	} {
		done, err := step.sync(ctx)
		if err != nil {
			return fmt.Errorf("同步出厂%s: %w", step.name, err)
		}
		logSync(logger, step.name, done)
	}
	return nil
}

func logSync(logger zerolog.Logger, what string, done []builtinsync.Action) {
	if len(done) == 0 {
		logger.Info().Msgf("出厂%s已是最新", what)
		return
	}
	names := map[builtinsync.Op]string{
		builtinsync.OpInsert: "新增", builtinsync.OpUpdate: "更新", builtinsync.OpBaseline: "补记基线",
		builtinsync.OpRetire: "下线删除", builtinsync.OpDetach: "下线转自建",
	}
	ev := logger.Info()
	for _, a := range done {
		ev = ev.Str(a.Code, names[a.Op])
	}
	ev.Msgf("出厂%s已同步", what)
}
