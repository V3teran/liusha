package finding

import (
	"encoding/json"
	"testing"
)

// 指纹规范形：大小写/尾斜杠/空白从宽归一——同义报告必须同键；
// 类别不同不得同键。
func TestClaimFingerprintCanonicalKeyNormalization(t *testing.T) {
	codec := FingerprintNormalizer{}
	a, _ := codec.CanonicalKey(json.RawMessage(`{"class":"Reflected_XSS","method":"get","host":"T.Local:8080","path":"/vuln/xss_r/","param":" Name "}`))
	b, _ := codec.CanonicalKey(json.RawMessage(`{"class":"reflected_xss","method":"GET","host":"t.local:8080","path":"/vuln/xss_r","param":"name"}`))
	if a == "" || a != b {
		t.Fatalf("同义指纹应同键: %q vs %q", a, b)
	}
	c, _ := codec.CanonicalKey(json.RawMessage(`{"class":"stored_xss","method":"GET","host":"t.local:8080","path":"/vuln/xss_r","param":"name"}`))
	if c == a {
		t.Fatal("不同类别不得同键")
	}

	// 形状损坏：不参与去重（ok=false），门按无指纹处理
	if _, ok := codec.CanonicalKey(json.RawMessage(`["not","an","object"]`)); ok {
		t.Fatal("损坏形状应返回 ok=false")
	}
}
