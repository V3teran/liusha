package seed

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/llm"
)

// llm.go：把 config.yaml 的 providers:/llm.* 静态配置 insert-only 首填进 llm_provider /
// llm_role_route 两表（migration 0097 + 0099）。
//
// 迁移语义（对齐 agent/ 种子）：DB 是事实源，种子只填**缺失行**——
// provider 按 key、role_route 按 role 判存在，已存在一律跳过，
// 绝不覆盖前端「模型」模块或运维在 DB 里的改动。
//
// 路由种子（agent → complexity 档 → provider；agent 档位在 agent.complexity，前端可改）：
//   - llm.complexity_routes.{simple,medium,complex} → 各档路由行（role 列存档名）
// 种子只写这 3 档 + 1 备胎，不再 per-agent 摊平（agent 档位在 agent.complexity，无需入库）。
//
// 安全：provider.api_key_env 只搬**环境变量名**，密钥值不经手（本就不在 yaml 里）。

// ImportLLM 把 cfg 里的 provider/角色路由 insert-only 首填进 DB。
// 顺序遵守 FK：provider → role_route（引用 provider）。
// cfg.Providers 为空（DB 已成事实源、yaml 不再带 providers）时整体 no-op。
func ImportLLM(ctx context.Context, cfg config.Config, s *llmcfg.Store) error {
	// 首次启动门：llm_provider 非空即视为已完成引导，整段跳过——DB 是运行期唯一事实源，
	// UI 删除的 provider 不得被 yaml 复活；yaml 只服务「空库冷启动」与「灾难后重建」。
	existing, err := s.ListProviders(ctx, false)
	if err != nil {
		return fmt.Errorf("查已有 provider: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}
	// provider 标识符由系统生成 UUID（yaml key 降级为显示名称默认值）；
	// 路由 complexity_routes 按 yaml key 引用 provider，导入路由时翻译成生成的标识符。
	codeByYamlKey, err := importProviders(ctx, cfg, s)
	if err != nil {
		return fmt.Errorf("import providers: %w", err)
	}
	if err := importRoleRoutes(ctx, cfg, codeByYamlKey, s); err != nil {
		return fmt.Errorf("import role routes: %w", err)
	}
	return nil
}

// importProviders 建 provider 部署行：标识符一律系统生成 UUID（yaml key 降级为显示名称默认值）。
// 返回 yaml key → 生成标识符 的映射，供路由导入翻译 complexity_routes 的引用。
func importProviders(ctx context.Context, cfg config.Config, s *llmcfg.Store) (map[string]string, error) {
	codeByYamlKey := make(map[string]string, len(cfg.Providers))
	for key, p := range cfg.Providers {
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
		code := uuid.NewString()
		codeByYamlKey[key] = code
		if _, err := s.CreateProvider(ctx, llmcfg.ProviderParams{
			Code:                 code,
			DisplayName:          key,
			Type:                 typ,
			BaseURL:              p.BaseURL,
			DefaultModel:         p.DefaultModel,
			LegacyAPIKeyEnv:      p.APIKeyEnv,
			MaxTokens:            p.MaxTokens,
			SupportsTools:        p.SupportsTools,
			SupportsVision:       supportsVision,
			ContextWindow:        contextWindow,
			ThinkingBudgetTokens: p.ThinkingBudgetTokens,
			Enabled:              true,
		}); err != nil {
			return nil, fmt.Errorf("建 provider %q: %w", key, err)
		}
	}
	return codeByYamlKey, nil
}

// importRoleRoutes 按 role insert-only 建路由行：3 个复杂度档（simple/medium/complex）。
// role 列存档名；agent 档位在 agent.complexity，不入路由表。
// 目标 provider 不存在（对应档槽位空或 provider 未建）时跳过该行——避免撞 FK RESTRICT。
func importRoleRoutes(ctx context.Context, cfg config.Config, codeByYamlKey map[string]string, s *llmcfg.Store) error {
	existing, err := s.ListRoleRoutes(ctx)
	if err != nil {
		return fmt.Errorf("列角色路由: %w", err)
	}
	routeSet := make(map[string]bool, len(existing))
	for _, rr := range existing {
		routeSet[rr.Role] = true
	}

	// 三档，key 直接是 complexity 名，value 是 provider key（无 field-name 间接层）。
	merged := map[string]string{
		llmcfg.ComplexitySimple:  cfg.LLM.ComplexityRoutes[llmcfg.ComplexitySimple],
		llmcfg.ComplexityMedium:  cfg.LLM.ComplexityRoutes[llmcfg.ComplexityMedium],
		llmcfg.ComplexityComplex: cfg.LLM.ComplexityRoutes[llmcfg.ComplexityComplex],
	}

	for role, providerKey := range merged {
		if routeSet[role] {
			continue // 已存在→跳过（insert-only）
		}
		if providerKey == "" {
			continue // 目标 provider 未配置（槽位空）→ 跳过
		}
		// complexity_routes 按 yaml key 引用 provider，翻译成实际生成的标识符。
		code, ok := codeByYamlKey[providerKey]
		if !ok {
			continue // 目标 provider 未建 → 跳过，避免撞 FK RESTRICT
		}
		if _, err := s.UpsertRoleRoute(ctx, role, code); err != nil {
			return fmt.Errorf("建角色路由 %q→%q: %w", role, code, err)
		}
		routeSet[role] = true
	}
	return nil
}
