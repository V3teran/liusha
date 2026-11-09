// Package settingstore 是系统设置（会话压缩 / 工具运行时 / 代理流量过滤三组）的读写入口。
//
// 生效值 = 出厂默认叠加用户自定义：出厂默认由代码与 config.yaml 的部署值算出（各进程
// 挂载同一份配置，算出的默认值一致），数据库 setting_override 只存用户改过的字段。
// 读取经三级缓存（进程内存 L1 → redis L2 → 数据库），只缓存用户层；写入后广播失效，
// runner 下次现读即新值，proxy 收到 proxy_filter 失效后热换过滤链。
package settingstore

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/overridestore"
)

// Defaults 是三组设置的出厂默认。
type Defaults struct {
	Compaction  CompactionSettings
	Runtime     RuntimeSettings
	ProxyFilter ProxyFilterSettings
}

// DefaultsFrom 由部署配置算出出厂默认。
func DefaultsFrom(cfg config.AppConfig) Defaults {
	p := cfg.Proxy
	return Defaults{
		Compaction: DefaultCompaction(),
		Runtime:    DefaultRuntime(cfg.Runner.StepLLMTimeoutSeconds),
		ProxyFilter: ProxyFilterSettings{
			AllowHosts:              p.AllowHosts,
			ExcludeMethods:          p.ExcludeMethods,
			ExcludeHosts:            p.ExcludeHosts,
			ExcludeUpgradeProtocols: p.ExcludeUpgradeProtocols,
			ExcludeSuffixes:         p.ExcludeSuffixes,
			ExcludeContentTypes:     p.ExcludeContentTypes,
			ExcludeStatusCodes:      p.ExcludeStatusCodes,
			MaxRequestBodySize:      p.MaxRequestBodySize,
			MaxResponseBodySize:     p.MaxResponseBodySize,
		}.normalized(),
	}
}

// Store 是系统设置的读写入口。
type Store struct {
	table    overridestore.Table
	cache    *cachestore.Cache
	defaults Defaults
}

// New 构造 Store。cache 由进程唯一构造并已在后台 Subscribe。
func New(pool *pgxpool.Pool, cache *cachestore.Cache, defaults Defaults) *Store {
	return &Store{
		table:    overridestore.Table{Pool: pool, Name: "setting_override", Key: "group_key"},
		cache:    cache,
		defaults: defaults,
	}
}

// overrideKey 是某组用户层的缓存键。
func overrideKey(g Group) string { return "settingstore:override:" + string(g) }

// KeyProxyFilter 是 proxy_filter 组的缓存键：proxy 的失效回调据此判断是否需要热换过滤链。
var KeyProxyFilter = overrideKey(GroupProxyFilter)

// ── 读 ──────────────────────────────────────────────────────────────

// Compaction 读会话历史压缩设置的生效值。
func (s *Store) Compaction(ctx context.Context) (CompactionSettings, error) {
	var v CompactionSettings
	err := s.effective(ctx, GroupCompaction, &v)
	return v, err
}

// Runtime 读工具运行时设置的生效值。
func (s *Store) Runtime(ctx context.Context) (RuntimeSettings, error) {
	var v RuntimeSettings
	err := s.effective(ctx, GroupRuntime, &v)
	return v, err
}

// ProxyFilter 读代理流量过滤规则的生效值。
func (s *Store) ProxyFilter(ctx context.Context) (ProxyFilterSettings, error) {
	var v ProxyFilterSettings
	err := s.effective(ctx, GroupProxyFilter, &v)
	return v, err
}

// GroupDetail 是设置页需要的一组信息：生效值、出厂默认、各已自定义字段状态。
type GroupDetail struct {
	Values    override.Document         `json:"values"`
	Defaults  override.Document         `json:"defaults"`
	Overrides map[string]override.State `json:"overrides"`
}

// Detail 返回一组设置的生效值、出厂默认与自定义状态。
func (s *Store) Detail(ctx context.Context, g Group) (GroupDetail, error) {
	schema, def, err := s.layout(g)
	if err != nil {
		return GroupDetail{}, err
	}
	ov, err := s.overrides(ctx, g)
	if err != nil {
		return GroupDetail{}, err
	}
	return GroupDetail{
		Values:    override.Apply(schema, def, ov),
		Defaults:  def,
		Overrides: override.Describe(schema, def, ov),
	}, nil
}

// ── 写 ──────────────────────────────────────────────────────────────

// Patch 写入用户提交的字段（数值为新值，名单为 {on, off}），校验应用后的生效值。
func (s *Store) Patch(ctx context.Context, g Group, changes override.Document) error {
	return s.update(ctx, g, func(schema override.Schema, def override.Document, cur override.Overrides) (override.Overrides, error) {
		return override.Patch(schema, def, cur, changes)
	})
}

// Reset 把指定字段恢复为出厂默认（不传字段则整组恢复）。
func (s *Store) Reset(ctx context.Context, g Group, fields ...string) error {
	return s.update(ctx, g, func(schema override.Schema, _ override.Document, cur override.Overrides) (override.Overrides, error) {
		for _, f := range fields {
			if _, ok := schema[f]; !ok {
				return nil, fmt.Errorf("%w：字段 %q 不可自定义", override.ErrInvalid, f)
			}
		}
		return override.Reset(cur, fields...), nil
	})
}

func (s *Store) update(ctx context.Context, g Group, edit func(override.Schema, override.Document, override.Overrides) (override.Overrides, error)) error {
	schema, def, err := s.layout(g)
	if err != nil {
		return err
	}
	if _, err := s.table.Update(ctx, string(g), func(_ context.Context, _ pgx.Tx, cur override.Overrides) (override.Overrides, error) {
		next, err := edit(schema, def, cur)
		if err != nil {
			return nil, err
		}
		return next, validate(g, override.Apply(schema, def, next))
	}); err != nil {
		return err
	}
	return s.cache.Invalidate(ctx, overrideKey(g))
}

// ── 内部 ────────────────────────────────────────────────────────────

func (s *Store) overrides(ctx context.Context, g Group) (override.Overrides, error) {
	key := overrideKey(g)
	return cachestore.ReadThrough(ctx, s.cache, key,
		func(override.Overrides) []string { return []string{key} },
		func(ctx context.Context) (override.Overrides, error) { return s.table.Get(ctx, string(g)) })
}

func (s *Store) effective(ctx context.Context, g Group, out any) error {
	schema, def, err := s.layout(g)
	if err != nil {
		return err
	}
	ov, err := s.overrides(ctx, g)
	if err != nil {
		return err
	}
	if err := override.FromDocument(override.Apply(schema, def, ov), out); err != nil {
		return fmt.Errorf("设置 %s: 合成生效值: %w", g, err)
	}
	return nil
}

// layout 返回一组设置的字段表与出厂默认文档：名单字段为集合，其余为整值替换。
func (s *Store) layout(g Group) (override.Schema, override.Document, error) {
	var v any
	switch g {
	case GroupCompaction:
		v = s.defaults.Compaction
	case GroupRuntime:
		v = s.defaults.Runtime
	case GroupProxyFilter:
		v = s.defaults.ProxyFilter
	default:
		return nil, nil, fmt.Errorf("%w：未知设置分组 %q", override.ErrInvalid, g)
	}
	def, err := override.ToDocument(v)
	if err != nil {
		return nil, nil, fmt.Errorf("设置 %s: 序列化出厂默认: %w", g, err)
	}
	schema := override.SchemaOf(v)
	return schema, def, nil
}

// validate 校验应用修改后的整组生效值。
func validate(g Group, doc override.Document) error {
	var problems []string
	switch g {
	case GroupCompaction:
		var c CompactionSettings
		if err := override.FromDocument(doc, &c); err != nil {
			return fmt.Errorf("%w：%w", override.ErrInvalid, err)
		}
		for name, r := range map[string]float64{
			"planner_context_ratio": c.PlannerContextRatio, "executor_context_ratio": c.ExecutorContextRatio,
			"evaluator_context_ratio": c.EvaluatorContextRatio, "monitor_context_ratio": c.MonitorContextRatio,
		} {
			if r <= 0 || r > maxContextRatio {
				problems = append(problems, fmt.Sprintf("%s 须在 (0, %.1f] 之间（历史预算占模型上下文窗口的比例）", name, maxContextRatio))
			}
		}
	case GroupRuntime:
		var r RuntimeSettings
		if err := override.FromDocument(doc, &r); err != nil {
			return fmt.Errorf("%w：%w", override.ErrInvalid, err)
		}
		for name, v := range map[string]int{
			"step_tool_timeout_seconds": r.StepToolTimeoutSeconds, "step_llm_timeout_seconds": r.StepLLMTimeoutSeconds,
			"run_tail_bytes": r.RunTailBytes, "tool_output_tail_bytes": r.ToolOutputTailBytes,
		} {
			if v <= 0 {
				problems = append(problems, name+" 必须 > 0")
			}
		}
	case GroupProxyFilter:
		var p ProxyFilterSettings
		if err := override.FromDocument(doc, &p); err != nil {
			return fmt.Errorf("%w：%w", override.ErrInvalid, err)
		}
		if p.MaxRequestBodySize <= 0 {
			problems = append(problems, "max_request_body_size 必须 > 0（请求体切片上限字节）")
		}
		if p.MaxResponseBodySize <= 0 {
			problems = append(problems, "max_response_body_size 必须 > 0（响应体切片上限字节）")
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("%w：%s", override.ErrInvalid, strings.Join(problems, "；"))
	}
	return nil
}
