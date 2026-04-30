package llm

import "testing"

func TestClientPool_OpenAICompatSingleton(t *testing.T) {
	p := NewClientPool()
	c1, err := p.GetOrCreateOpenAI("https://api.deepseek.com/v1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := p.GetOrCreateOpenAI("https://api.deepseek.com/v1", "k1")
	if c1 != c2 {
		t.Fatal("same baseURL+key should return same client")
	}
	c3, _ := p.GetOrCreateOpenAI("https://api.deepseek.com/v1", "k2")
	if c1 == c3 {
		t.Fatal("different api key should return different client")
	}
}

func TestClientPool_AnthropicSingleton(t *testing.T) {
	p := NewClientPool()
	c1, _ := p.GetOrCreateAnthropic("https://api.anthropic.com", "k1")
	c2, _ := p.GetOrCreateAnthropic("https://api.anthropic.com", "k1")
	if c1 != c2 {
		t.Fatal("same baseURL+key should return same client")
	}
}

func TestClientPool_EmptyAPIKey_Errors(t *testing.T) {
	p := NewClientPool()
	if _, err := p.GetOrCreateOpenAI("", ""); err == nil {
		t.Error("empty apikey should error (openai)")
	}
	if _, err := p.GetOrCreateAnthropic("", ""); err == nil {
		t.Error("empty apikey should error (anthropic)")
	}
}

func TestClientPool_DifferentBaseURL(t *testing.T) {
	p := NewClientPool()
	c1, _ := p.GetOrCreateOpenAI("https://a.example/v1", "k")
	c2, _ := p.GetOrCreateOpenAI("https://b.example/v1", "k")
	if c1 == c2 {
		t.Error("different baseURL should return different client")
	}
}
