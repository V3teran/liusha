package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_FailsFastWhenPostgresDSNMissing(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")

	_, err := config.Load(configPath(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres_dsn")
}

func TestLoad_ReadsFromEnvAndYAML(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_API_ADDR", "0.0.0.0:8080")
	t.Setenv("LIUSHA_ENV", "development")
	t.Setenv("LIUSHA_LOG_LEVEL", "debug")
	t.Setenv("LIUSHA_DEEPSEEK_API_KEY", "sk-test")

	cfg, err := config.Load(configPath(t))

	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@localhost:5432/db?sslmode=disable", cfg.PostgresDSN)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, "0.0.0.0:8080", cfg.APIAddr)
	assert.Equal(t, "development", cfg.Env)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 60, cfg.AnalysisWindow.TimeSeconds)
	assert.Equal(t, 500, cfg.AnalysisWindow.FlowCount)
}

func TestLoad_RejectsUnknownEnv(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_ENV", "staging-yolo")

	_, err := config.Load(configPath(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "env")
}

// TestLoad_LLMDefaults 验证未显式配置 LLM 时，使用 deepseek-chat + 官方 BaseURL 等默认值。
func TestLoad_LLMDefaults(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_DEEPSEEK_API_KEY", "sk-test")
	// 其余 LLM 字段不设置，走默认

	cfg, err := config.Load(configPath(t))

	require.NoError(t, err)
	assert.Equal(t, "sk-test", cfg.LLM.APIKey)
	assert.Equal(t, "https://api.deepseek.com", cfg.LLM.BaseURL)
	assert.Equal(t, "deepseek-chat", cfg.LLM.DefaultModel)
	assert.Equal(t, 4096, cfg.LLM.MaxTokens)
	assert.Equal(t, 5, cfg.LLM.MaxSteps)
}

// TestLoad_LLMRequiresAPIKey 验证缺失 API Key 时快速失败。
func TestLoad_LLMRequiresAPIKey(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_DEEPSEEK_API_KEY", "")

	_, err := config.Load(configPath(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deepseek_api_key")
}

// TestLoad_LLMCustomBaseURL 验证可通过 env 覆盖 BaseURL（支持 OneAPI/LiteLLM 网关）。
func TestLoad_LLMCustomBaseURL(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("LIUSHA_REDIS_ADDR", "localhost:6379")
	t.Setenv("LIUSHA_DEEPSEEK_API_KEY", "sk-test")
	t.Setenv("LIUSHA_LLM_BASE_URL", "https://oneapi.example.com/v1")
	t.Setenv("LIUSHA_LLM_DEFAULT_MODEL", "deepseek-reasoner")

	cfg, err := config.Load(configPath(t))

	require.NoError(t, err)
	assert.Equal(t, "https://oneapi.example.com/v1", cfg.LLM.BaseURL)
	assert.Equal(t, "deepseek-reasoner", cfg.LLM.DefaultModel)
}

func configPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "config", "config.yaml")
}
