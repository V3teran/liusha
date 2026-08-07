package seed

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// llm.go：把 config.yaml 的 providers:/llm.* 静态配置 insert-only 首填进 llm_provider /
// llm_role_route 两表（migration 0097 + 0099）。
//
// 迁移语义（对齐 hunter/scenario 种子）：DB 是事实源，种子只填**缺失行**——
// provider 按 key、role_route 按 role 判存在，已存在一律跳过，
// 绝不覆盖前端「模型」模块或运维在 DB 里的改动。
//
// 路由还原（0099 拆别名层后：role → provider **一跳直连**）：
//   - llm.agents/utilities 的 role → field-name（如 "vision_provider"）→ 该 field 对应的 provider key
//     （field-name 只是 yaml 里指向 llm.*_provider 槽位的间接层，解引用到真实 provider key）
//   - llm.default_provider  → 保留 role __default__（role 未命中兜底）
//   - llm.fallback_provider → 保留 role __fallback__（retry 耗尽备胎）
//
// 安全：provider.api_key_env 只搬**环境变量名**，密钥值不经手（本就不在 yaml 里）。

// fieldToProviderKey 把 yaml 里的 field-name 间接值（"vision_provider" 等）解引用到真实 provider key。
// 未知 field-name 返回空串，调用方据此报错（不静默丢路由）。
func fieldToProviderKey(field string, cfg config.Config) string {
	switch field {
	case "default_provider":
		return cfg.LLM.DefaultProvider
	case "light_provider":
		return cfg.LLM.LightProvider
	case "vision_provider":
		return cfg.LLM.VisionProvider
	case "fallback_provider":
		return cfg.LLM.FallbackProvider
	default:
		return ""
	}
}

// ImportLLM 把 cfg 里的 provider/角色路由 insert-only 首填进 DB。
// 顺序遵守 FK：provider → role_route（引用 provider）。
// cfg.Providers 为空（DB 已成事实源、yaml 不再带 providers）时整体 no-op。
func ImportLLM(ctx context.Context, cfg config.Config, s *llmcfg.Store) error {
	if err := importProviders(ctx, cfg, s); err != nil {
		return fmt.Errorf("import providers: %w", err)
	}
	if err := importRoleRoutes(ctx, cfg, s); err != nil {
		return fmt.Errorf("import role routes: %w", err)
	}
	return nil
}

// importProviders 按 key insert-only 建 provider 部署行。
func importProviders(ctx context.Context, cfg config.Config, s *llmcfg.Store) error {
	for key, p := range cfg.Providers {
		if _, err := s.GetProvider(ctx, key); err == nil {
			continue // 已存在→跳过（insert-only）
		} else if !notFound(err) {
			return fmt.Errorf("查 provider %q: %w", key, err)
		}
		typ := p.Type
		if typ == "" {
			typ = llmcfg.ProviderTypeOpenAICompat // 空 type 视为 openai_compat（与运行期一致）
		}
		// SupportsVision/ContextWindow 在 config.validate 已强制非 nil；仍防御性解引用。
		supportsVision := p.SupportsVision != nil && *p.SupportsVision
		contextWindow := 0
		if p.ContextWindow != nil {
			contextWindow = *p.ContextWindow
		}
		if _, err := s.CreateProvider(ctx, llmcfg.ProviderParams{
			Key:            key,
			Type:           typ,
			BaseURL:        p.BaseURL,
			DefaultModel:   p.DefaultModel,
			APIKeyEnv:      p.APIKeyEnv,
			MaxTokens:      p.MaxTokens,
			SupportsTools:  p.SupportsTools,
			SupportsVision: supportsVision,
			ContextWindow:  contextWindow,
			Enabled:        true,
		}); err != nil {
			return fmt.Errorf("建 provider %q: %w", key, err)
		}
	}
	return nil
}

// importRoleRoutes 按 role insert-only 建角色路由（role → provider key 直连），合并 agents + utilities，
// 并把 default/fallback 全局槽折成保留 role __default__ / __fallback__。
// 目标 provider 不存在（对应 llm.*_provider 槽位空或未建）时跳过该 role——避免撞 FK RESTRICT。
func importRoleRoutes(ctx context.Context, cfg config.Config, s *llmcfg.Store) error {
	existing, err := s.ListRoleRoutes(ctx)
	if err != nil {
		return fmt.Errorf("列角色路由: %w", err)
	}
	routeSet := make(map[string]bool, len(existing))
	for _, rr := range existing {
		routeSet[rr.Role] = true
	}

	// 业务 role：agents（真 agent）+ utilities（single-shot 工具）同一路由语义，合并处理；
	// 值是 field-name 间接层，需解引用到真实 provider key。
	merged := make(map[string]string, len(cfg.LLM.Agents)+len(cfg.LLM.Utilities)+2)
	for role, field := range cfg.LLM.Agents {
		merged[role] = fieldToProviderKey(field, cfg)
		if merged[role] == "" {
			return fmt.Errorf("角色 %q 的路由值 %q 未知（应为 *_provider）", role, field)
		}
	}
	for role, field := range cfg.LLM.Utilities {
		merged[role] = fieldToProviderKey(field, cfg)
		if merged[role] == "" {
			return fmt.Errorf("角色 %q 的路由值 %q 未知（应为 *_provider）", role, field)
		}
	}
	// 两个全局槽 → 保留 role（直接取 provider key，非 field-name）。
	merged[llmcfg.RoleDefault] = cfg.LLM.DefaultProvider
	merged[llmcfg.RoleFallback] = cfg.LLM.FallbackProvider

	for role, providerKey := range merged {
		if routeSet[role] {
			continue // 已存在→跳过（insert-only）
		}
		if providerKey == "" {
			continue // 目标 provider 未配置（槽位空）→ 跳过
		}
		if _, err := s.GetProvider(ctx, providerKey); err != nil {
			if notFound(err) {
				continue // 目标 provider 未建 → 跳过，避免撞 FK RESTRICT
			}
			return fmt.Errorf("查角色 %q 目标 provider %q: %w", role, providerKey, err)
		}
		if _, err := s.UpsertRoleRoute(ctx, role, providerKey); err != nil {
			return fmt.Errorf("建角色路由 %q→%q: %w", role, providerKey, err)
		}
		routeSet[role] = true
	}
	return nil
}
