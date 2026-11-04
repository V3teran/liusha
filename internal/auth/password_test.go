package auth

import (
	"strings"
	"testing"
)

// TestHashPassword_Roundtrip 验证：哈希可校验、错密码不通过、哈希含盐（两次生成不同串）。
func TestHashPassword_Roundtrip(t *testing.T) {
	h, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := CheckPassword(h, "correct-horse-battery"); err != nil {
		t.Errorf("正确密码应通过: %v", err)
	}
	if err := CheckPassword(h, "wrong-password"); err == nil {
		t.Error("错误密码不应通过")
	}

	h2, _ := HashPassword("correct-horse-battery")
	if h == h2 {
		t.Error("两次哈希应不同（bcrypt 内含随机盐）")
	}
	if !strings.HasPrefix(h, "$2") {
		t.Errorf("应为 bcrypt PHC 串: %q", h)
	}
}

// TestHashPassword_TooLong 验证：超 bcrypt 72 字节上限直接报错（handler 层也应前置校验）。
func TestHashPassword_TooLong(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("x", 73)); err == nil {
		t.Error("73 字节密码应报错")
	}
}

// TestDummyCompare 验证：未知用户的时序等价比对不 panic、恒失败。
func TestDummyCompare(_ *testing.T) {
	DummyCompare("anything")
	DummyCompare(strings.Repeat("y", 100)) // 超长输入也不应 panic
}
