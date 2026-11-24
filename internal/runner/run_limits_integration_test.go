//go:build integration

package runner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/settingstore"
)

// 一轮 run 生效的系统配置：读得到用户层时就是它（没有任何自定义时即出厂值）；读不到用户层
// （库故障）时回落到出厂值——与没有任何自定义时同一个取值，不留零值。出厂值取自配置文件的
// runtime / compaction 段，代码里没有另一套数。
func TestRunLimits_FallBackToFactoryWhenStoreIsUnreadable(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load(filepath.Join("..", "config", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	factory := settingstore.DefaultsFrom(cfg)
	// 出厂值确实来自配置文件：改掉配置里的数，出厂值跟着变。
	cfg.Runtime.StepToolTimeoutSeconds = 900
	cfg.Compaction.ExecutorContextRatio = 0.7
	factory = settingstore.DefaultsFrom(cfg)
	if factory.Runtime.StepToolTimeoutSeconds != 900 || factory.Compaction.ExecutorContextRatio != 0.7 {
		t.Fatalf("出厂值应取自配置文件的 runtime / compaction 段，实际 %+v %+v", factory.Runtime, factory.Compaction)
	}

	newCache := func() *cachestore.Cache {
		return cachestore.New(redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}), 0)
	}
	pool := dbtest.NewPgPool(t)
	h := &Handler{Settings: settingstore.New(pool, newCache(), factory)}
	got := h.runLimits(ctx, zerolog.Nop())
	if got.Compaction != factory.Compaction || got.Runtime != factory.Runtime {
		t.Fatalf("没有自定义时应为出厂值，实际 %+v %+v", got.Compaction, got.Runtime)
	}

	// 库不可用：换一个没有缓存可命中的 store，读用户层必然失败。
	pool.Close()
	h = &Handler{Settings: settingstore.New(pool, newCache(), factory)}
	got = h.runLimits(ctx, zerolog.Nop())
	if got.Compaction != factory.Compaction || got.Runtime != factory.Runtime {
		t.Fatalf("读不到用户层时应回落到出厂值，实际 %+v %+v", got.Compaction, got.Runtime)
	}
	if got.Runtime.StepToolTimeoutSeconds <= 0 || got.Runtime.ToolOutputTailBytes <= 0 || got.Compaction.PlannerContextRatio <= 0 {
		t.Fatalf("回落值不得是零值（零值会让工具输出完全不做长度钳制），实际 %+v %+v", got.Compaction, got.Runtime)
	}

}
