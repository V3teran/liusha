package llm

import "testing"

// TestUsage_Add 校验 Usage.Add 是值方法且各字段累加正确
func TestUsage_Add(t *testing.T) {
	a := Usage{InTokens: 100, OutTokens: 50, CachedTokens: 10}
	b := Usage{InTokens: 200, OutTokens: 80, CachedTokens: 20}

	got := a.Add(b)
	want := Usage{InTokens: 300, OutTokens: 130, CachedTokens: 30}
	if got != want {
		t.Fatalf("Add 结果错误: got %+v want %+v", got, want)
	}

	// 值方法不应修改 receiver
	if a != (Usage{InTokens: 100, OutTokens: 50, CachedTokens: 10}) {
		t.Fatalf("Add 不应修改 receiver: got %+v", a)
	}
}

// TestUsage_AddZero 验证零值加任意值不改变结果
func TestUsage_AddZero(t *testing.T) {
	a := Usage{InTokens: 5, OutTokens: 7, CachedTokens: 3}
	got := a.Add(Usage{})
	if got != a {
		t.Fatalf("加零值应等于自身: got %+v want %+v", got, a)
	}
}

// TestRole_Constants 校验 Role 常量字符串值
func TestRole_Constants(t *testing.T) {
	cases := []struct {
		role Role
		want string
	}{
		{RoleSystem, "system"},
		{RoleUser, "user"},
		{RoleAssistant, "assistant"},
		{RoleTool, "tool"},
	}
	for _, c := range cases {
		if string(c.role) != c.want {
			t.Errorf("Role 常量错误: got %q want %q", string(c.role), c.want)
		}
	}
}
