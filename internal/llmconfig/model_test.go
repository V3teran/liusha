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
		{"max_tokens 为 0（未经归一）", func(p *ProviderParams) { p.MaxTokens = 0 }, false},
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
	cases := []struct {
		name string
		p    Provider
		want bool
	}{
		{"已加密落库", Provider{EncryptedAPIKey: []byte("x")}, true},
		{"未设置密钥", Provider{}, false},
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

// 上下文窗口与单次最大输出没填（0）时，归一落成缺省——缺省只有一个出处，落库的行不再有
// 「未设置」；已填的值原样保留。扩展思考预算按落成后的上限判定。
func TestProviderParamsNormalize_FillsLimitDefaults(t *testing.T) {
	p := ProviderParams{Type: ProviderTypeAnthropic, BaseURL: "https://x", DisplayName: "n", DefaultModel: "m"}
	p.Normalize()
	if p.MaxTokens != DefaultMaxTokens || p.ContextWindow != DefaultContextWindow {
		t.Fatalf("没填的上限应落成缺省 %d / %d，实际 %d / %d", DefaultMaxTokens, DefaultContextWindow, p.MaxTokens, p.ContextWindow)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("落成缺省后应合法: %v", err)
	}
	budget := p
	budget.ThinkingBudgetTokens = 65536
	if err := budget.Validate(); err != nil {
		t.Fatalf("思考预算小于缺省上限应合法: %v", err)
	}
	budget.ThinkingBudgetTokens = int64(DefaultMaxTokens)
	if err := budget.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("思考预算不小于缺省上限应被拒，got %v", err)
	}
	kept := ProviderParams{Type: ProviderTypeAnthropic, BaseURL: "https://x", MaxTokens: 4096, ContextWindow: 200000}
	kept.Normalize()
	if kept.MaxTokens != 4096 || kept.ContextWindow != 200000 {
		t.Fatalf("已填的值不应被缺省覆盖: %+v", kept)
	}
}

// 缺省取当前旗舰模型的官方上限：上下文 1M、单次输出 128K。它们只是上限，不多花钱；
// 上限小于它的模型，上游拒绝输出上限时运行期自动下调（见 framework/llm）。
func TestLimitDefaults(t *testing.T) {
	if DefaultContextWindow != 1048576 || DefaultMaxTokens != 131072 {
		t.Fatalf("缺省应为 1048576 / 131072，实际 %d / %d", DefaultContextWindow, DefaultMaxTokens)
	}
}
