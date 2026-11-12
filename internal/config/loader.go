package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// defaultYAML 是编进程序的默认配置：二进制开箱即用，部署无需挂配置文件。
//
//go:embed config.yaml
var defaultYAML []byte

// Load 加载配置：内置默认 → 覆盖文件（path 非空时，只需写改动的键）→ LIUSHA_ 环境变量，
// 反序列化、应用默认值并校验，含 LLM 种子形状校验（见 validateLLMSeed；不校验密钥是否就位）。
// 调 LLM 的组件（runner / api）用它。
func Load(path string) (AppConfig, error) { return load(path, true) }

// LoadWithoutLLM 与 Load 同，但跳过 LLM 种子校验。给不调 LLM 的组件（proxy、sandbox）用——
// LLM 配置与它们无关，写错也不该挡它们启动。
func LoadWithoutLLM(path string) (AppConfig, error) { return load(path, false) }

func load(path string, withLLM bool) (AppConfig, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	// 内置默认含全部键：AutomaticEnv 只覆盖已存在的键，默认里有，环境变量就一定生效。
	if err := v.ReadConfig(bytes.NewReader(defaultYAML)); err != nil {
		return AppConfig{}, fmt.Errorf("read embedded default config: %w", err)
	}
	if path != "" {
		v.SetConfigFile(path)
		if err := v.MergeInConfig(); err != nil {
			return AppConfig{}, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	var c AppConfig
	if err := v.Unmarshal(&c); err != nil {
		return AppConfig{}, fmt.Errorf("unmarshal config: %w", err)
	}
	c.ApplyDefaults()
	if err := validate(c); err != nil {
		return AppConfig{}, err
	}
	if withLLM {
		if err := validateLLMSeed(c); err != nil {
			return AppConfig{}, err
		}
	}
	return c, nil
}

// validateLLMSeed 校验 yaml 内 LLM 种子的形状自洽性。仅调 LLM 的进程（runner / api）
// 需要——proxy 用 LoadWithoutLLM 跳过。
//
// 事实源是 DB（前端「LLM 配置」页改 llm_provider/llm_role_route），yaml 仅首次
// insert-only 种子（见 seed.ImportLLM）。故 providers 留空 = 完全依赖 DB，此时跳过全部校验，
// 不再强制 complexity_routes.medium——否则空 yaml + 满 DB 的正常部署会被误判 fail-fast。
// providers 非空时按声明校验：形状必须完整（路由引用存在 + api_key_env 声明），
// 密钥是否就位不在此校验——「先部署、后在前端配置」由三层防线保证：任务下发
// 边界拒绝（dispatch.requireLLMReady，未就绪 400 带指引）、运行期未配置致命化
// （framework/llm.ErrNotConfigured → 任务干净终结）、cron 到点跳过待下轮。
// 缺档由运行期就近降级接管（见 framework/llm routeCandidates）。换厂商 = 改
// config.yaml 的 provider 块 + .env 放对应 key。
func validateLLMSeed(c AppConfig) error {
	if len(c.Providers) == 0 {
		return nil
	}
	// 形状校验：三档路由必须引用已声明的 provider 块，且每块声明 api_key_env
	// （medium 是隐式默认档——未归档的调用者与上下文压缩都走它，故必须显式路由）。
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
	}
	return nil
}
