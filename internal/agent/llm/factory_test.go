package llm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/agent/llm"
)

// TestNewDeepSeek_RejectsEmptyAPIKey 空 api key 必须 fail-fast。
func TestNewDeepSeek_RejectsEmptyAPIKey(t *testing.T) {
	_, err := llm.NewDeepSeek("", "https://api.deepseek.com", "deepseek-chat", 1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api key")
}

// TestNewDeepSeek_RejectsEmptyBaseURL 空 base url 必须 fail-fast。
func TestNewDeepSeek_RejectsEmptyBaseURL(t *testing.T) {
	_, err := llm.NewDeepSeek("sk-xxx", "", "deepseek-chat", 1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "base url")
}
