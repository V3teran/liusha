package cryptx

import (
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const plaintext = "sk-real-secret-value-12345"
	sealed, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(sealed) == plaintext {
		t.Fatal("sealed value must not equal plaintext")
	}
	got, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestEncryptProducesDistinctNoncesEachCall(t *testing.T) {
	c, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, err := c.Encrypt("same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt a: %v", err)
	}
	b, err := c.Encrypt("same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt b: %v", err)
	}
	if string(a) == string(b) {
		t.Fatal("同一明文两次加密应产出不同密文（随机 nonce）")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c1, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New c1: %v", err)
	}
	c2, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New c2: %v", err)
	}
	sealed, err := c1.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(sealed); err == nil {
		t.Fatal("用错误密钥解密应失败")
	}
}

func TestDecryptTruncatedValueFails(t *testing.T) {
	c, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Decrypt([]byte("too-short")); err == nil {
		t.Fatal("过短的密文应报错，不应 panic 或返回垃圾明文")
	}
}

func TestNewRejectsWrongKeySize(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("密钥长度不对应报错")
	}
}
