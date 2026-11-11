package seed

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// llm.go：把 config.yaml 的 providers:/llm.* 静态配置 insert-only 首填进 llm_provider /
// llm_role_route 两表。
//
// 首装引导语义：LLM 接入是用户数据，DB 是事实源，种子只填**缺失行**——
// provider 按 key、role_route 按 role 判存在，已存在一律跳过，
// 绝不覆盖前端「LLM 配置」页或运维在 DB 里的改动。
//
// 路由种子（agent → complexity 档 → provider；agent 档位在 agent.complexity，前端可改）：
//   - llm.complexity_routes.{simple,medium,complex} → 各档路由行（role 列存档名）
// 种子只写这 3 档（agent 档位在 agent 生效配置的 complexity，不入路由表）。
//
// 安全：provider.api_key_env 只搬**环境变量名**，密钥值不经手（本就不在 yaml 里）。

// ImportLLM 把 cfg 里的 provider/角色路由 insert-only 首填进 DB。
// 顺序遵守 FK：provider → role_route（引用 provider）。
// cfg.Providers 为空（DB 已成事实源、yaml 不再带 providers）时整体 no-op。
func ImportLLM(ctx context.Context, cfg config.AppConfig, s *llmconfig.Store) error {
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

// importProviders 建 provider 行：标识符一律系统生成 UUID（yaml key 降级为显示名称默认值）。
// 返回 yaml key → 生成标识符 的映射，供路由导入翻译 complexity_routes 的引用。
func importProviders(ctx context.Context, cfg config.AppConfig, s *llmconfig.Store) (map[string]string, error) {
	codeByYamlKey := make(map[string]string, len(cfg.Providers))
	for key, p := range cfg.Providers {
		// 密钥拿不到的 provider 不种子（如部署未配 GLM_API_KEY 而用户要用其他厂商）：
		// 无密钥的 provider 行不可用，种下去是死行；跳过后引用它的角色路由由
		// importRoleRoutes 自行跳过（避免撞 FK）。启动后在「LLM 配置」页添加任意
		// provider（openai_compat / anthropic）并设路由即可。
		if p.APIKeyEnv != "" && os.Getenv(p.APIKeyEnv) == "" {
			continue
		}
		typ := p.Type
		if typ == "" {
			typ = llmconfig.ProviderTypeOpenAICompat // yaml 缺省 type 视为 openai_compat（DB 约束不收空值）
		}
		// SupportsVision/ContextWindow 在 config.validate 已强制非 nil；仍防御性解引用。
		supportsVision := p.SupportsVision != nil && *p.SupportsVision
		contextWindow := 0
		if p.ContextWindow != nil {
			contextWindow = *p.ContextWindow
		}
		code := uuid.NewString()
		codeByYamlKey[key] = code
		if _, err := s.CreateProvider(ctx, llmconfig.ProviderParams{
			Code:                 code,
			DisplayName:          key,
			Type:                 typ,
			BaseURL:              p.BaseURL,
			DefaultModel:         p.DefaultModel,
			APIKeyEnv:            p.APIKeyEnv,
			AuthScheme:           p.AuthScheme,
			OutputLimitParam:     p.OutputLimitParam,
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
func importRoleRoutes(ctx context.Context, cfg config.AppConfig, codeByYamlKey map[string]string, s *llmconfig.Store) error {
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
		llmconfig.ComplexitySimple:  cfg.LLM.ComplexityRoutes[llmconfig.ComplexitySimple],
		llmconfig.ComplexityMedium:  cfg.LLM.ComplexityRoutes[llmconfig.ComplexityMedium],
		llmconfig.ComplexityComplex: cfg.LLM.ComplexityRoutes[llmconfig.ComplexityComplex],
	}
	roles := []string{
		llmconfig.ComplexitySimple,
		llmconfig.ComplexityMedium,
		llmconfig.ComplexityComplex,
	}

	// 1) 显式路由优先：目标 provider 已建（密钥就位）→ 直接落行。
	for _, role := range roles {
		if routeSet[role] {
			continue // 已存在→跳过（insert-only）
		}
		code, ok := codeByYamlKey[merged[role]]
		if !ok {
			continue // 目标 provider 未建（密钥缺失被跳过）→ 走 2) 降级补齐
		}
		if _, err := s.UpsertRoleRoute(ctx, role, code); err != nil {
			return fmt.Errorf("建角色路由 %q→%q: %w", role, code, err)
		}
		routeSet[role] = true
	}

	// 2) 就近降级补齐：provider 少于三档时（如只配 1-2 把 key），缺档取能力相邻
	//    档的 provider——complex 缺→medium→simple，medium 缺→complex→simple，
	//    simple 缺→medium→complex（与 llm.Router 运行期就近降级同序）。DB 三档齐全，
	//    前端「能力路由」所见即所得。
	for _, role := range roles {
		if routeSet[role] {
			continue
		}
		for _, alt := range roleDegradeOrder(role) {
			if code, ok := codeByYamlKey[merged[alt]]; ok {
				if _, err := s.UpsertRoleRoute(ctx, role, code); err != nil {
					return fmt.Errorf("建角色路由 %q→%q（降级）: %w", role, code, err)
				}
				routeSet[role] = true
				break
			}
		}
	}

	// 3) 兜底：路由完全没对上的极端情形（只配了一块而 routes 写的是别的名字）
	//    → 三档全指唯一可用的 provider（确定性：yaml key 排序取首）。
	if len(codeByYamlKey) > 0 {
		keys := make([]string, 0, len(codeByYamlKey))
		for k := range codeByYamlKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fallbackCode := codeByYamlKey[keys[0]]
		for _, role := range roles {
			if !routeSet[role] {
				if _, err := s.UpsertRoleRoute(ctx, role, fallbackCode); err != nil {
					return fmt.Errorf("建角色路由 %q→%q（兜底）: %w", role, fallbackCode, err)
				}
			}
		}
	}
	return nil
}

// roleDegradeOrder 返回档位的就近降级序（能力相邻档优先，不含自身）。
func roleDegradeOrder(role string) []string {
	switch role {
	case llmconfig.ComplexityComplex:
		return []string{llmconfig.ComplexityMedium, llmconfig.ComplexitySimple}
	case llmconfig.ComplexitySimple:
		return []string{llmconfig.ComplexityMedium, llmconfig.ComplexityComplex}
	default: // medium：默认档，向上（能力更强）优先于向下
		return []string{llmconfig.ComplexityComplex, llmconfig.ComplexitySimple}
	}
}
