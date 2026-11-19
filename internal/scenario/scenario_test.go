package scenario

import "testing"

func TestRender(t *testing.T) {
	if got := Render("渗透测试", "  正文\n第二行  \n"); got != "<scenario name=\"渗透测试\">\n正文\n第二行\n</scenario>" {
		t.Errorf("Render = %q", got)
	}
	if got := Render("名", "   "); got != "" {
		t.Errorf("无正文应为空: %q", got)
	}
	if got := Render(`a"b`+"\nc", "x"); got != "<scenario name=\"a'b c\">\nx\n</scenario>" {
		t.Errorf("名称中的引号与换行不应破坏标签: %q", got)
	}
}

func TestAppend(t *testing.T) {
	if got := Append("章程", ""); got != "章程" {
		t.Errorf("无场景时提示词应原样: %q", got)
	}
	if got := Append("", "<scenario>"); got != "" {
		t.Errorf("提示词为空时保持空（由调用方报错）: %q", got)
	}
	if got := Append("章程", "块"); got != "章程\n\n块" {
		t.Errorf("Append = %q", got)
	}
}
