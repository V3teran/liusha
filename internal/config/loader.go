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
func Load(path string) (Config, error) { return load(path, true) }

// LoadWithoutLLMKeys 与 Load 同，但跳过 LLM provider key 校验。
// 给纯 ingress 进程（cmd/proxy 仅 MITM + XADD，从不调 LLM）用——避免强塞一堆用不到的 key 才能启动。
func LoadWithoutLLMKeys(path string) (Config, error) { return load(path, false) }

func load(path string, requireLLMKeys bool) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	c.ApplyDefaults()
	if err := validate(c); err != nil {
		return Config{}, err
	}
	if requireLLMKeys {
		if err := validateLLMKeys(c); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

// ApplyDefaults 把所有 sub-struct 的 0 值字段补默认。可重复调用幂等。
//
// 单独导出便于测试：测试用例 `c := Config{}; c.ApplyDefaults()` 即可拿到全默认配置。

func validateLLMKeys(c Config) error {
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
		{c.LLM.Fallback, "fallback"},
	} {
		if pair.name != "" {
			if err := check(pair.name, pair.role); err != nil {
				return err
			}
		}
	}
	return nil
}
