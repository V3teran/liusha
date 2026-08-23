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
// 迁移语义（对齐 agent/scenario 种子）：DB 是事实源，种子只填**缺失行**——
// provider 按 key、role_route 按 role 判存在，已存在一律跳过，
// 绝不覆盖前端「模型」模块或运维在 DB 里的改动。
//
// 路由种子（0105 分档：agent → tier → provider 两跳，agent→tier 固定在代码 llmcfg.AgentTier）：
//   - llm.tiers.{heavy,vision,light} → 各档路由行（role 列存 tier 名）
//   - llm.fallback                   → 保留 role __fallback__（retry 耗尽备胎）
// 种子只写这 3 档 + 1 备胎，不再 per-agent 摊平（agent→tier 由代码定，无需入库）。
//
// 安全：provider.api_key_env 只搬**环境变量名**，密钥值不经手（本就不在 yaml 里）。

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
			Key:             key,
			Type:            typ,
			BaseURL:         p.BaseURL,
			DefaultModel:    p.DefaultModel,
			LegacyAPIKeyEnv: p.APIKeyEnv,
			MaxTokens:       p.MaxTokens,
			SupportsTools:   p.SupportsTools,
			SupportsVision:  supportsVision,
			ContextWindow:   contextWindow,
			Enabled:         true,
		}); err != nil {
			return fmt.Errorf("建 provider %q: %w", key, err)
		}
	}
	return nil
}

// importRoleRoutes 按 role insert-only 建路由行：3 个能力档（heavy/vision/light）+ 保留 role __fallback__。
// role 列存 tier 名（heavy/vision/light）或 __fallback__；agent→tier 的绑定固定在代码，不入库。
// 目标 provider 不存在（对应 tier 槽位空或 provider 未建）时跳过该行——避免撞 FK RESTRICT。
func importRoleRoutes(ctx context.Context, cfg config.Config, s *llmcfg.Store) error {
	existing, err := s.ListRoleRoutes(ctx)
	if err != nil {
		return fmt.Errorf("列角色路由: %w", err)
	}
	routeSet := make(map[string]bool, len(existing))
	for _, rr := range existing {
		routeSet[rr.Role] = true
	}

	// 三档 + 备胎，key 直接是 complexity 名 / 保留 role，value 是 provider key（无 field-name 间接层）。
	merged := map[string]string{
		llmcfg.ComplexitySimple:  cfg.LLM.Tiers[llmcfg.ComplexitySimple],
		llmcfg.ComplexityMedium:  cfg.LLM.Tiers[llmcfg.ComplexityMedium],
		llmcfg.ComplexityComplex: cfg.LLM.Tiers[llmcfg.ComplexityComplex],
		llmcfg.RoleFallback:      cfg.LLM.Fallback,
	}

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
