// Package config 用 viper 加载 YAML 配置 + ENV 覆盖 + 启动校验。
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config 是 liusha 顶层配置树。
type Config struct {
	API        APIConfig                 `mapstructure:"api"`
	Postgres   PostgresConfig            `mapstructure:"postgres"`
	LLM        LLMConfig                 `mapstructure:"llm"`
	Providers  map[string]ProviderConfig `mapstructure:"providers"`
	Proxy      ProxyConfig               `mapstructure:"proxy"`
	Engagement EngagementConfig          `mapstructure:"engagement"`
	Skills     SkillsConfig              `mapstructure:"skills"`
}

type APIConfig struct {
	ReadTimeoutSeconds  int `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
}

type PostgresConfig struct {
	MaxConns int `mapstructure:"max_conns"`
	MinConns int `mapstructure:"min_conns"`
}

// LLMConfig 包含主/轻/视觉/降级 4 个 provider 字段 + 路由表。
// light_provider / fallback_provider / routes 是黑客松借鉴新增（创新 11 + 共识 E）。
type LLMConfig struct {
	DefaultProvider  string            `mapstructure:"default_provider"`
	LightProvider    string            `mapstructure:"light_provider"`
	VisionProvider   string            `mapstructure:"vision_provider"`
	FallbackProvider string            `mapstructure:"fallback_provider"`
	MaxSteps         int               `mapstructure:"max_steps"`
	MaxTokensPerCall int               `mapstructure:"max_tokens_per_call"`
	Routes           map[string]string `mapstructure:"routes"`
}

// ProviderConfig 一个 LLM provider 的连接参数。
//
//	Type 取值:
//	  "openai_compat" 走 OpenAI 协议族（OpenAI/DeepSeek/Qwen/Moonshot/Groq/智谱/豆包 等）；
//	  "anthropic"     走 Anthropic 原生 /v1/messages；
//	  ""              视为 openai_compat（向后兼容）。
type ProviderConfig struct {
	Type           string `mapstructure:"type"`
	BaseURL        string `mapstructure:"base_url"`
	DefaultModel   string `mapstructure:"default_model"`
	VisionModel    string `mapstructure:"vision_model"`
	APIKeyEnv      string `mapstructure:"api_key_env"`
	MaxTokens      int    `mapstructure:"max_tokens"`
	SupportsTools  bool   `mapstructure:"supports_tools"`
	SupportsVision bool   `mapstructure:"supports_vision"`
}

// ProxyConfig 控制 in-process MITM 切片 + 责任链过滤 + 聚合参数。
//   - AllowHosts:           HostFilter 白名单（空=放行全部）
//   - ExcludeMethods:       HTTP 方法黑名单（OPTIONS/HEAD/CONNECT 等）
//   - ExcludeHosts:         Host 黑名单（支持 *.example.com）
//   - ExcludeSuffixes:      URL 后缀黑名单（.css/.js/图片字体等）
//   - ExcludeContentTypes:  Content-Type 黑名单（image/* 等）
//   - OnlyStatusCodes:      仅放行的状态码（空=放行全部）
//   - MaxRequestBodySize:   请求体大小上限（字节，0=不限）
//   - MaxResponseBodySize:  响应体大小上限（字节，0=不限）
type ProxyConfig struct {
	WindowBatch         int      `mapstructure:"window_batch"`
	WindowMaxAgeSeconds int      `mapstructure:"window_max_age_seconds"`
	AllowHosts          []string `mapstructure:"allow_hosts"`
	ExcludeMethods      []string `mapstructure:"exclude_methods"`
	ExcludeHosts        []string `mapstructure:"exclude_hosts"`
	ExcludeSuffixes     []string `mapstructure:"exclude_suffixes"`
	ExcludeContentTypes []string `mapstructure:"exclude_content_types"`
	OnlyStatusCodes     []int    `mapstructure:"only_status_codes"`
	MaxRequestBodySize  int      `mapstructure:"max_request_body_size"`
	MaxResponseBodySize int      `mapstructure:"max_response_body_size"`
}

type EngagementConfig struct {
	IdleTimeoutHours       int `mapstructure:"idle_timeout_hours"`
	SweeperIntervalSeconds int `mapstructure:"sweeper_interval_seconds"`
}

type SkillsConfig struct {
	Root string `mapstructure:"root"`
}

// Load 从 path 读取 YAML，应用 LIUSHA_ ENV 覆盖，反序列化并校验。
func Load(path string) (Config, error) {
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
	if err := validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validate 强制：default_provider 必填，light/vision/fallback 选填但配了就必须 check 通过。
func validate(c Config) error {
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
	if c.LLM.DefaultProvider == "" {
		return fmt.Errorf("llm.default_provider required")
	}
	if err := check(c.LLM.DefaultProvider, "default_provider"); err != nil {
		return err
	}
	for _, pair := range []struct{ name, role string }{
		{c.LLM.LightProvider, "light_provider"},
		{c.LLM.VisionProvider, "vision_provider"},
		{c.LLM.FallbackProvider, "fallback_provider"},
	} {
		if pair.name != "" {
			if err := check(pair.name, pair.role); err != nil {
				return err
			}
		}
	}
	return nil
}
