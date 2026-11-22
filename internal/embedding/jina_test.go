package embedding

import (
	"strings"
	"testing"
)

// NormalizeBaseURL 把用户填的服务地址归一为统一基础地址：操作路径、末尾斜杠、版本段
// 都收敛掉——「粘了完整端点」与「只粘主机」落成同一结果，客户端再拼 /v1/embeddings。
func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.jina.ai":                      "https://api.jina.ai",
		"https://api.jina.ai/":                     "https://api.jina.ai",
		"https://api.jina.ai/v1":                   "https://api.jina.ai",
		"https://api.jina.ai/v1/embeddings":        "https://api.jina.ai",
		"https://api.jina.ai/v1/rerank":            "https://api.jina.ai",
		"https://api.jina.ai/v1/embeddings/":       "https://api.jina.ai",
		"https://gw.example.com/openai/v1":         "https://gw.example.com/openai",
		"https://gw.example.com/openai/embeddings": "https://gw.example.com/openai",
		" https://api.jina.ai/v1 ":                 "https://api.jina.ai",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// NewClient 按配置解析默认值并拼出端点；空模型名落出厂默认，密钥为空拒绝构造。
func TestNewClient_ResolvesDefaultsAndEndpoints(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("无密钥应拒绝构造")
	}
	c, err := NewClient(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.EmbedURL() != DefaultBaseURL+"/v1/embeddings" || c.RerankURL() != DefaultBaseURL+"/v1/rerank" {
		t.Fatalf("默认端点错误: %s / %s", c.EmbedURL(), c.RerankURL())
	}
	if c.model != EmbeddingModelName || c.rerankModel != RerankModelName {
		t.Fatalf("默认模型名错误: %s / %s", c.model, c.rerankModel)
	}

	custom, err := NewClient(Config{APIKey: "k", BaseURL: "https://gw.example.com/openai/v1/", EmbeddingModel: "e-x", RerankModel: "r-x"})
	if err != nil {
		t.Fatal(err)
	}
	if custom.EmbedURL() != "https://gw.example.com/openai/v1/embeddings" {
		t.Fatalf("自定义基础地址应保留路径段: %s", custom.EmbedURL())
	}
	if custom.model != "e-x" {
		t.Fatalf("自定义模型名应生效: %s", custom.model)
	}
	if !strings.HasPrefix(custom.EmbedURL(), "https://") {
		t.Fatalf("端点应为完整 URL: %s", custom.EmbedURL())
	}
}
