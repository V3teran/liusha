package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// 协议必填的输出上限由调用方给值：框架不替它猜一个缺省——缺省只有一个出处（业务侧在配置
// 入口落定），再放一个数就会与它漂移。
func TestNewAnthropic_RequiresOutputLimit(t *testing.T) {
	client := anthropic.NewClient(option.WithAPIKey("test"))
	pool := NewClientPool()
	for name, cfg := range map[string]AnthropicConfig{
		"没给": {Model: "m"},
		"为零": {Model: "m", Limit: pool.OutputLimitFor("https://x", "k", "m", 0)},
	} {
		if _, err := NewAnthropic(context.Background(), "p", cfg, &client); err == nil || !strings.Contains(err.Error(), "输出上限必填") {
			t.Errorf("%s输出上限应报错，实际 %v", name, err)
		}
	}
	if _, err := NewOpenAICompat(context.Background(), "p", OpenAICompatConfig{Model: "m"}, openai.NewClient("test")); err == nil || !strings.Contains(err.Error(), "输出上限必填") {
		t.Errorf("Chat Completions 没给输出上限应报错，实际 %v", err)
	}
}
