//go:build integration

package dbtest

import (
	"context"
	"testing"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

const migration0269 uint = 269

// 迁移 0269：接入 OpenAI Responses 协议——type 的约束多一个取值；输出上限字段的约束改为随协议
// 落定（Responses 只有 max_output_tokens，Anthropic Messages 只有 max_tokens，Chat Completions
// 两个都可能）。约束与 framework/llm 的取值表对照。
func TestMigration0269_ResponsesProtocol(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0269)
	// 显示名称不区分大小写唯一：每个用例一个名字
	insert := func(code, protocol, field string) error {
		_, err := pool.Exec(ctx, `INSERT INTO llm_provider (code, type, base_url, default_model, context_window, display_name, auth_scheme, supports_vision, output_limit_param) VALUES ($1, $2, 'https://x', 'm', 1048576, $1, 'bearer', false, $3)`, code, protocol, field)
		return err
	}
	if err := insert("00000000-0000-4000-8000-000000000031", "openai_chat_completions", "max_tokens"); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0269)

	// 三种协议各自认的字段都可写
	for i, tc := range []struct{ protocol, field string }{
		{"openai_chat_completions", "max_tokens"},
		{"openai_chat_completions", "max_completion_tokens"},
		{"openai_responses", "max_output_tokens"},
		{"anthropic_messages", "max_tokens"},
	} {
		code := "00000000-0000-4000-8000-00000000004" + string(rune('0'+i))
		if err := insert(code, tc.protocol, tc.field); err != nil {
			t.Errorf("%s + %s 应可写: %v", tc.protocol, tc.field, err)
		}
		// 约束里的落定规则与代码同口径
		if want := fwllm.OutputLimitParamOf(tc.protocol, fwllm.OutputLimitParam(tc.field)); tc.protocol != "openai_chat_completions" && string(want) != tc.field {
			t.Errorf("代码里 %s 的输出上限字段是 %s，库里却接受了 %s", tc.protocol, want, tc.field)
		}
	}
	// 他协议的字段被拒
	for i, tc := range []struct{ protocol, field string }{
		{"openai_responses", "max_tokens"},
		{"anthropic_messages", "max_output_tokens"},
		{"anthropic_messages", "max_completion_tokens"},
		{"openai_chat_completions", "max_output_tokens"},
	} {
		code := "00000000-0000-4000-8000-00000000005" + string(rune('0'+i))
		if err := insert(code, tc.protocol, tc.field); err == nil {
			t.Errorf("%s 不应接受 %s", tc.protocol, tc.field)
		}
	}
}
