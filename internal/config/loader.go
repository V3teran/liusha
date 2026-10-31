package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Load 从 path 读取 YAML，应用 LIUSHA_ ENV 覆盖，反序列化、应用默认值并校验。
// Load 读配置 + 应用默认 + 完整校验（含 LLM provider key 在环境变量里非空）。
// 调 LLM 的进程（runner / api）用它。
func Load(path string) (AppConfig, error) { return load(path, true) }

// LoadWithoutLLMKeys 与 Load 同，但跳过 LLM provider key 校验。
// 给纯 ingress 进程（cmd/proxy 仅 MITM + XADD，从不调 LLM）用——避免强塞一堆用不到的 key 才能启动。
func LoadWithoutLLMKeys(path string) (AppConfig, error) { return load(path, false) }

func load(path string, requireLLMKeys bool) (AppConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return AppConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var c AppConfig
	if err := v.Unmarshal(&c); err != nil {
		return AppConfig{}, fmt.Errorf("unmarshal config: %w", err)
	}
	c.ApplyDefaults()
	if err := validate(c); err != nil {
		return AppConfig{}, err
	}
	if requireLLMKeys {
		if err := validateLLMKeys(c); err != nil {
			return AppConfig{}, err
		}
	}
	return c, nil
}

// validateLLMKeys 校验 yaml 内 LLM 种子的自洽性。仅调 LLM 的进程（runner / api）需要——
// proxy 用 LoadWithoutLLMKeys 跳过。
//
// 事实源是 DB（前端「模型/能力分档」模块 CRUD 改 llm_provider/llm_role_route），yaml 仅首次
// insert-only 种子（见 seed.ImportLLM）。故 providers 留空 = 完全依赖 DB，此时跳过全部校验，
// 不再强制 complexity_routes.medium——否则空 yaml + 满 DB 的正常部署会被误判 fail-fast。
// providers 一旦非空则按老规矩校验（medium 档必填 + 各档 api_key_env 在 ENV 非空），保证种子可用。
func validateLLMKeys(c AppConfig) error {
	if len(c.Providers) == 0 {
		return nil
	}
	check := func(name, role string) error {
		p, ok := c.Providers[name]
		if !ok {
			return fmt.Errorf("%s %q not in providers", role, name)
		}
		if p.APIKeyEnv == "" {
			return fmt.Errorf("%s %q: api_key_env not configured", role, name)
		}
		if os.Getenv(p.APIKeyEnv) == "" {
			return fmt.Errorf("env %s empty (required for %s=%s)", p.APIKeyEnv, role, name)
		}
		return nil
	}
	// medium 是隐式默认档（agent 未显式归档即落 medium），故种子必须配它。
	medium := c.LLM.ComplexityRoutes["medium"]
	if medium == "" {
		return fmt.Errorf("llm.complexity_routes.medium required")
	}
	if err := check(medium, "complexity_routes.medium"); err != nil {
		return err
	}
	for _, pair := range []struct{ name, role string }{
		{c.LLM.ComplexityRoutes["simple"], "complexity_routes.simple"},
		{c.LLM.ComplexityRoutes["complex"], "complexity_routes.complex"},
	} {
		if pair.name != "" {
			if err := check(pair.name, pair.role); err != nil {
				return err
			}
		}
	}
	return nil
}
