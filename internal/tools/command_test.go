package tools

import (
	"strings"
	"testing"
	"time"
)

func TestRunCommandDesc_ListenerContract(t *testing.T) {
	// 未发布监听段：明示回连类操作不可达——LLM 不知道就会绑 4444 白跑一步。
	plain := newRunCommandTool(Deps{}, time.Minute)
	if got := plain.Desc(); !strings.Contains(got, "回连的操作不可达") {
		t.Fatalf("未发布段时应明示回连不可达: %q", got)
	}

	// 已发布段 + 回连地址：必须把「绑哪个段、回连写什么」一次说清。
	tool := newRunCommandTool(Deps{
		ListenerPortStart: 21000,
		ListenerPortCount: 100,
		CallbackHost:      "203.0.113.7",
	}, time.Minute)
	desc := tool.Desc()
	for _, want := range []string{"21000-21099", "203.0.113.7"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述应含 %q: %q", want, desc)
		}
	}

	// 已发布段但回连地址未配置：显式告知缺失，防 LLM 编造地址。
	noHost := newRunCommandTool(Deps{ListenerPortStart: 21000, ListenerPortCount: 100}, time.Minute)
	if got := noHost.Desc(); !strings.Contains(got, "callback_host") {
		t.Fatalf("回连地址缺失时应指向 sandbox.callback_host: %q", got)
	}
}
