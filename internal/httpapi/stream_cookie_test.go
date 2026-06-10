package httpapi

import (
	"testing"
	"time"
)

func TestStreamCookie_SignVerify_RoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long-xxxxxx")
	tok := signStreamToken(secret, "conv-123", time.Now().Add(30*time.Minute))
	if tok == "" {
		t.Fatal("signStreamToken 返回空")
	}
	if !verifyStreamToken(secret, tok, "conv-123") {
		t.Error("同 convID 同密钥应校验通过")
	}
}

func TestStreamCookie_Reject(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long-xxxxxx")
	valid := signStreamToken(secret, "conv-123", time.Now().Add(30*time.Minute))

	cases := map[string]bool{
		"错 convID": verifyStreamToken(secret, valid, "conv-999"),
		"错密钥":      verifyStreamToken([]byte("wrong-secret-32-bytes-long-yyyy"), valid, "conv-123"),
		"篡改 token": verifyStreamToken(secret, valid+"x", "conv-123"),
		"过期":       verifyStreamToken(secret, signStreamToken(secret, "conv-123", time.Now().Add(-time.Minute)), "conv-123"),
		"空 token":  verifyStreamToken(secret, "", "conv-123"),
		"垃圾格式":     verifyStreamToken(secret, "not.a.valid.token", "conv-123"),
	}
	for name, got := range cases {
		if got {
			t.Errorf("%s 应校验失败，却通过", name)
		}
	}
}
