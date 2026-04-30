package done_validator

import "testing"

// TestRegistry_BACDefault —— bac_v1 应在包初始化时自动注册。
func TestRegistry_BACDefault(t *testing.T) {
	if !IsRegistered("bac_v1") {
		t.Fatal("bac_v1 应在 init 时自动注册")
	}
}

// TestRegistry_UnknownKey —— 未注册 key 应返回 false。
func TestRegistry_UnknownKey(t *testing.T) {
	if IsRegistered("nope_v999") {
		t.Fatal("未注册 key 不应返回 true")
	}
}

// TestRegistry_ManualRegister —— Register 后 IsRegistered 应返回 true。
func TestRegistry_ManualRegister(t *testing.T) {
	const k = "test_v1_marker"
	if IsRegistered(k) {
		t.Fatalf("前置：%q 不应预先注册", k)
	}
	Register(k)
	if !IsRegistered(k) {
		t.Fatalf("Register 后 %q 应被识别", k)
	}
}
