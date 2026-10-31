package seed

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/settingstore"
)

// system.go：把系统业务旋钮（compaction 会话压缩 / runtime 工具运行时 / proxy_filter
// 流量过滤规则）insert-only 首填进 system_setting（migration 0098）三行分组 KV。
//
// 迁移语义（对齐 agent//llm 种子）：DB 是事实源，种子只填**缺失组**——
// 每组按 group_key 判存在（GetXxxRaw 绕缓存直穿 DB），缺行才写；已存在一律跳过，
// 绝不覆盖前端「系统配置」或运维在 DB 里的改动。三组彼此独立，各自判存。
// compaction 组无 yaml 源（内置默认）；runtime 组仅 LLM 看门狗默认取 yaml；proxy_filter 组取 yaml 过滤字段。

// ImportSystem 把 cfg 里的 compaction/runtime/proxy_filter 三组旋钮 insert-only 首填进 DB。
// 每组独立判存（缺组才写），任一组已存在则跳过该组。
func ImportSystem(ctx context.Context, cfg config.AppConfig, s *settingstore.Store) error {
	if err := importCompactionSettings(ctx, s); err != nil {
		return fmt.Errorf("import compaction settings: %w", err)
	}
	if err := importRuntimeSettings(ctx, cfg, s); err != nil {
		return fmt.Errorf("import runtime settings: %w", err)
	}
	if err := importProxyFilterSettings(ctx, cfg, s); err != nil {
		return fmt.Errorf("import proxy_filter settings: %w", err)
	}
	return nil
}

// importCompactionSettings 缺 compaction 组才用内置默认首填（窗口条数无 yaml 源——
// DB 即事实源，normalized 兜底与 DefaultCompaction 一致）。
func importCompactionSettings(ctx context.Context, s *settingstore.Store) error {
	if _, err := s.GetCompactionRaw(ctx); err == nil {
		return nil // 已存在→跳过（insert-only）
	} else if !settingstore.IsNotFound(err) {
		return fmt.Errorf("查 compaction 组: %w", err)
	}
	if err := s.SaveCompaction(ctx, settingstore.DefaultCompaction()); err != nil {
		return fmt.Errorf("写 compaction 组: %w", err)
	}
	return nil
}

// importRuntimeSettings 缺 runtime 组才首填：LLM 看门狗默认取 yaml runner.step_llm_timeout_seconds
// （唯一保留的 yaml 源），其余字段用内置默认。
func importRuntimeSettings(ctx context.Context, cfg config.AppConfig, s *settingstore.Store) error {
	if _, err := s.GetRuntimeRaw(ctx); err == nil {
		return nil
	} else if !settingstore.IsNotFound(err) {
		return fmt.Errorf("查 runtime 组: %w", err)
	}
	if err := s.SaveRuntime(ctx, settingstore.DefaultRuntime(cfg.Runner.StepLLMTimeoutSeconds)); err != nil {
		return fmt.Errorf("写 runtime 组: %w", err)
	}
	return nil
}

// importProxyFilterSettings 缺 proxy_filter 组才用 yaml 的 proxy.* 过滤字段首填。
func importProxyFilterSettings(ctx context.Context, cfg config.AppConfig, s *settingstore.Store) error {
	if _, err := s.GetProxyFilterRaw(ctx); err == nil {
		return nil
	} else if !settingstore.IsNotFound(err) {
		return fmt.Errorf("查 proxy_filter 组: %w", err)
	}
	p := cfg.Proxy
	if err := s.SaveProxyFilter(ctx, settingstore.ProxyFilterSettings{
		AllowHosts:              p.AllowHosts,
		ExcludeMethods:          p.ExcludeMethods,
		ExcludeHosts:            p.ExcludeHosts,
		ExcludeUpgradeProtocols: p.ExcludeUpgradeProtocols,
		ExcludeSuffixes:         p.ExcludeSuffixes,
		ExcludeContentTypes:     p.ExcludeContentTypes,
		ExcludeStatusCodes:      p.ExcludeStatusCodes,
		MaxRequestBodySize:      p.MaxRequestBodySize,
		MaxResponseBodySize:     p.MaxResponseBodySize,
	}); err != nil {
		return fmt.Errorf("写 proxy_filter 组: %w", err)
	}
	return nil
}
