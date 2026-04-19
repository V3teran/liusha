package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	PostgresDSN    string
	RedisAddr      string
	APIAddr        string
	Env            string
	LogLevel       string
	AnalysisWindow AnalysisWindowConfig
	API            APIConfig
	Postgres       PostgresConfig
	Proxy          ProxyConfig
}

type AnalysisWindowConfig struct {
	TimeSeconds int
	FlowCount   int
}

type APIConfig struct {
	ReadTimeoutSeconds  int
	WriteTimeoutSeconds int
}

type PostgresConfig struct {
	MaxConns int
	MinConns int
}

// ProxyConfig 是 cmd/proxy 专用配置；cmd/api 不使用。
type ProxyConfig struct {
	Addr         string // 代理监听地址，如 0.0.0.0:8888
	FlowFile     string // proxify 输出的 JSONL 文件路径
	BatchSize    int    // 批量写 Postgres 的条数阈值
	FlushSeconds int    // 批量写的时间间隔（秒）
}

var validEnvs = map[string]struct{}{
	"development": {},
	"production":  {},
	"test":        {},
}

func Load(yamlPath string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(yamlPath)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", yamlPath, err)
	}

	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := Config{
		PostgresDSN: v.GetString("POSTGRES_DSN"),
		RedisAddr:   v.GetString("REDIS_ADDR"),
		APIAddr:     v.GetString("API_ADDR"),
		Env:         v.GetString("ENV"),
		LogLevel:    v.GetString("LOG_LEVEL"),
		AnalysisWindow: AnalysisWindowConfig{
			TimeSeconds: v.GetInt("analysis_window.time_seconds"),
			FlowCount:   v.GetInt("analysis_window.flow_count"),
		},
		API: APIConfig{
			ReadTimeoutSeconds:  v.GetInt("api.read_timeout_seconds"),
			WriteTimeoutSeconds: v.GetInt("api.write_timeout_seconds"),
		},
		Postgres: PostgresConfig{
			MaxConns: v.GetInt("postgres.max_conns"),
			MinConns: v.GetInt("postgres.min_conns"),
		},
		Proxy: ProxyConfig{
			Addr:         v.GetString("proxy.addr"),
			FlowFile:     v.GetString("proxy.flow_file"),
			BatchSize:    v.GetInt("proxy.batch_size"),
			FlushSeconds: v.GetInt("proxy.flush_seconds"),
		},
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = "0.0.0.0:8080"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Env == "" {
		cfg.Env = "development"
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []string
	if c.PostgresDSN == "" {
		errs = append(errs, "postgres_dsn is required (set LIUSHA_POSTGRES_DSN)")
	}
	if c.RedisAddr == "" {
		errs = append(errs, "redis_addr is required (set LIUSHA_REDIS_ADDR)")
	}
	if _, ok := validEnvs[c.Env]; !ok {
		errs = append(errs, fmt.Sprintf("env must be one of development/production/test, got %q", c.Env))
	}
	if c.AnalysisWindow.TimeSeconds <= 0 {
		errs = append(errs, "analysis_window.time_seconds must be > 0")
	}
	if c.AnalysisWindow.FlowCount <= 0 {
		errs = append(errs, "analysis_window.flow_count must be > 0")
	}
	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}
