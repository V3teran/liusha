package llmconfig

import (
	"errors"
	"testing"
)

func validParams() ProviderParams {
	return ProviderParams{
		Code: "p", DisplayName: "主力", Type: ProviderTypeAnthropic, BaseURL: "https://api.example.com",
		DefaultModel: "m", ContextWindow: 128000, MaxTokens: 8192,
		AuthScheme: AuthXAPIKey, OutputLimitParam: OutputLimitMaxTokens,
	}
}

func TestProviderParamsValidate(t *testing.T) {
	cases := []struct {
		name  string
		mod   func(*ProviderParams)
		valid bool
	}{
		{"合法", func(*ProviderParams) {}, true},
		{"合法思考预算", func(p *ProviderParams) { p.ThinkingBudgetTokens = 4096 }, true},
		{"非法协议", func(p *ProviderParams) { p.Type = "foo" }, false},
		{"Base URL 无协议", func(p *ProviderParams) { p.BaseURL = "api.example.com" }, false},
		{"Base URL 非 http", func(p *ProviderParams) { p.BaseURL = "ftp://x" }, false},
		{"Base URL 无主机", func(p *ProviderParams) { p.BaseURL = "https://" }, false},
		{"显示名称空白", func(p *ProviderParams) { p.DisplayName = "  " }, false},
		{"默认模型空白", func(p *ProviderParams) { p.DefaultModel = "  " }, false},
		{"上下文窗口为 0", func(p *ProviderParams) { p.ContextWindow = 0 }, false},
		{"max_tokens 为负", func(p *ProviderParams) { p.MaxTokens = -1 }, false},
		{"思考预算为负", func(p *ProviderParams) { p.ThinkingBudgetTokens = -1 }, false},
		{"思考预算用于 openai 兼容", func(p *ProviderParams) { p.Type = ProviderTypeOpenAICompat; p.ThinkingBudgetTokens = 2048 }, false},
		{"思考预算低于下限", func(p *ProviderParams) { p.ThinkingBudgetTokens = 1023 }, false},
		{"思考预算等于 max_tokens", func(p *ProviderParams) { p.ThinkingBudgetTokens = 8192 }, false},
		{"max_tokens 缺省按 4096 判定", func(p *ProviderParams) { p.MaxTokens = 0; p.ThinkingBudgetTokens = 4096 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validParams()
			tc.mod(&p)
			err := p.Validate()
			if tc.valid && err != nil {
				t.Fatalf("应合法: %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalid) {
				t.Fatalf("应返回 ErrInvalid，got %v", err)
			}
		})
	}
}

func TestIsComplexity(t *testing.T) {
	for _, tier := range []string{ComplexitySimple, ComplexityMedium, ComplexityComplex} {
		if !IsComplexity(tier) {
			t.Fatalf("%s 应是合法档位", tier)
		}
	}
	if IsComplexity("planner") || IsComplexity("") {
		t.Fatal("非三档的名字不是合法档位")
	}
}

func TestProviderHasKey(t *testing.T) {
	t.Setenv("LIUSHA_TEST_KEY_EMPTY", "")
	t.Setenv("LIUSHA_TEST_KEY_SET", "sk")
	cases := []struct {
		name string
		p    Provider
		want bool
	}{
		{"已加密落库", Provider{EncryptedAPIKey: []byte("x")}, true},
		{"环境变量有值", Provider{APIKeyEnv: "LIUSHA_TEST_KEY_SET"}, true},
		{"环境变量为空", Provider{APIKeyEnv: "LIUSHA_TEST_KEY_EMPTY"}, false},
		{"无任何来源", Provider{}, false},
	}
	for _, tc := range cases {
		if got := tc.p.HasKey(); got != tc.want {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

// 地址归一只由协议规则推出：去尾斜杠、去误粘的操作路径；Anthropic 再去末尾 /v1。
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		protocol, in, want string
	}{
		{ProviderTypeOpenAICompat, " https://api.example.com/v1/ ", "https://api.example.com/v1"},
		{ProviderTypeOpenAICompat, "https://api.example.com/v1/chat/completions", "https://api.example.com/v1"},
		{ProviderTypeOpenAICompat, "https://api.example.com/v1/models", "https://api.example.com/v1"},
		{ProviderTypeOpenAICompat, "https://relay.example.com", "https://relay.example.com"},
		{ProviderTypeOpenAICompat, "https://api.example.com/anthropic", "https://api.example.com/anthropic"},
		{ProviderTypeAnthropic, "https://api.example.com/anthropic/v1/messages", "https://api.example.com/anthropic"},
		{ProviderTypeAnthropic, "https://api.example.com/v1", "https://api.example.com"},
		{ProviderTypeAnthropic, "https://api.example.com/anthropic/", "https://api.example.com/anthropic"},
		{ProviderTypeAnthropic, "not a url", "not a url"},
	}
	for _, tc := range cases {
		if got := NormalizeBaseURL(tc.protocol, tc.in); got != tc.want {
			t.Fatalf("%s %q: want %q, got %q", tc.protocol, tc.in, tc.want, got)
		}
	}
}

// 归一填充请求画像缺省值：鉴权方式取协议标准方式；anthropic 的输出上限字段恒为 max_tokens。
func TestProviderParamsNormalize(t *testing.T) {
	o := ProviderParams{Type: ProviderTypeOpenAICompat, BaseURL: "https://x/v1/", DefaultModel: " m "}
	o.Normalize()
	if o.AuthScheme != AuthBearer || o.OutputLimitParam != OutputLimitMaxTokens || o.BaseURL != "https://x/v1" || o.DefaultModel != "m" {
		t.Fatalf("openai 缺省归一错误: %+v", o)
	}
	a := ProviderParams{Type: ProviderTypeAnthropic, BaseURL: "https://x/v1", OutputLimitParam: OutputLimitMaxCompletionTokens}
	a.Normalize()
	if a.AuthScheme != AuthXAPIKey || a.OutputLimitParam != OutputLimitMaxTokens || a.BaseURL != "https://x" {
		t.Fatalf("anthropic 缺省归一错误: %+v", a)
	}
	kept := ProviderParams{Type: ProviderTypeAnthropic, BaseURL: "https://x", AuthScheme: AuthBearer}
	kept.Normalize()
	if kept.AuthScheme != AuthBearer {
		t.Fatal("已记录的鉴权方式不应被缺省值覆盖")
	}
	bad := validParams()
	bad.AuthScheme = "basic"
	if err := bad.Validate(); err == nil {
		t.Fatal("非法鉴权方式应被拒")
	}
}
