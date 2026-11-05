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
// providers 非空时按声明校验：形状必须完整（路由引用存在 + api_key_env 声明），
// 密钥至少一把就位（全缺 → fatal，即「完全不配置就无法使用」）；部分缺失不
// fatal，缺档由运行期就近降级接管（见 framework/llm routeCandidates）。换厂商 = 改 config.yaml 的 provider 块 + .env 放对应 key。
func validateLLMKeys(c AppConfig) error {
	if len(c.Providers) == 0 {
		return nil
	}
	// 形状校验：三档路由必须引用已声明的 provider 块，且每块声明 api_key_env
	//（medium 是隐式默认档——agent 未显式归档即落 medium，故必须显式路由）。
	shape := func(name, role string) error {
		p, ok := c.Providers[name]
		if !ok {
			return fmt.Errorf("%s %q not in providers", role, name)
		}
		if p.APIKeyEnv == "" {
			return fmt.Errorf("%s %q: api_key_env not configured", role, name)
		}
		return nil
	}
	medium := c.LLM.ComplexityRoutes["medium"]
	if medium == "" {
		return fmt.Errorf("llm.complexity_routes.medium required")
	}
	// 密钥契约：至少一个被路由引用的 env 就位（=「完全不配置就无法使用」的
	// 启动期体现）；部分缺失不 fatal——对应 provider 不参与种子，其档位由
	// Router 的就近降级规则接管（complex 缺→medium→simple，medium 缺→complex
	// →simple，simple 缺→medium→complex），多少把 key 都落地成一套可用路由。
	usable := 0
	for _, pair := range []struct{ name, role string }{
		{c.LLM.ComplexityRoutes["simple"], "complexity_routes.simple"},
		{medium, "complexity_routes.medium"},
		{c.LLM.ComplexityRoutes["complex"], "complexity_routes.complex"},
	} {
		if pair.name == "" {
			continue
		}
		if err := shape(pair.name, pair.role); err != nil {
			return err
		}
		if os.Getenv(c.Providers[pair.name].APIKeyEnv) != "" {
			usable++
		}
	}
	if usable == 0 {
		return fmt.Errorf(
			"llm: 所有路由档位的密钥 env 均为空——至少配置一把 LLM key" +
				"（providers 各块 api_key_env 指向的 env，按 .env 同名条目填写）")
	}
	return nil
}
