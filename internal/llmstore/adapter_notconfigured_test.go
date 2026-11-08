package llmstore

import (
	"context"
	"errors"
	"testing"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// 密钥来源缺失（密文与 env 皆空）是「未配置」而非故障：GetProviderSpec 必须带
// fwllm.ErrNotConfigured 哨兵上抛——下发边界与运行期致命化据 errors.Is 判定，
// 任务据此干净终结并给出配置指引，不进重试（实测缺失时任务永挂 active）。
func TestRouterStoreAdapter_MissingKeyWrapsNotConfigured(t *testing.T) {
	f := &fakeLLM{providers: []llmconfig.Provider{{
		Code: "p1", Type: "openai_compat", BaseURL: "https://x", DefaultModel: "m",
		Enabled: true,
		// EncryptedAPIKey nil、APIKeyEnv 空 → ResolveAPIKey 报「未配置密钥」
	}}}
	s := newLLMTestStore(t, f)
	a := s.AsRouterStore(nil) // cipher nil：密文为空的 provider 走不到解密
	_, err := a.GetProviderSpec(context.Background(), "p1")
	if !errors.Is(err, fwllm.ErrNotConfigured) {
		t.Fatalf("密钥缺失应透传 ErrNotConfigured 链，得 %v", err)
	}
}
