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

func configPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "config", "config.yaml")
}
