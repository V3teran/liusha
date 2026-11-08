package main

import (
	"strings"
	"testing"
)

func profileNames(ps []profile) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.name
	}
	return out
}

func TestSelectProfiles_ByName(t *testing.T) {
	got, err := selectProfiles([]string{"xss"})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if names := profileNames(got); len(names) != 1 || names[0] != "xss" {
		t.Fatalf("应只选中 xss，得 %v", names)
	}
}

func TestSelectProfiles_NoArgsSelectsAllSorted(t *testing.T) {
	got, err := selectProfiles(nil)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	names := profileNames(got)
	if len(names) != len(profiles) {
		t.Fatalf("无参应选中全部 %d 个 profile，得 %v", len(profiles), names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("应按字典序，得 %v", names)
		}
	}
}

func TestSelectProfiles_CaseInsensitiveAndDeduped(t *testing.T) {
	got, err := selectProfiles([]string{"XSS", " xss ", "full"})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if names := strings.Join(profileNames(got), ","); names != "xss,full" {
		t.Fatalf("应去重且保持参数顺序，得 %s", names)
	}
}

func TestSelectProfiles_UnknownListsKnown(t *testing.T) {
	_, err := selectProfiles([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "full") || !strings.Contains(err.Error(), "xss") {
		t.Fatalf("未知 profile 应报错并列出可选项，得 %v", err)
	}
}

func TestSelectProfiles_RejectsRetiredModePrefix(t *testing.T) {
	if _, err := selectProfiles([]string{"active:xss"}); err == nil {
		t.Fatal("已退役的 active: 前缀不应再被接受")
	}
}
