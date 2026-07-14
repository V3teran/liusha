package main

import "testing"

func TestSplitMarkdown(t *testing.T) {
	md := `# 标题一
正文 A 第一段。

## 子标题
正文 B。

## 另一节
正文 C。`
	chunks := splitMarkdown(md)
	if len(chunks) != 3 {
		t.Fatalf("应切成 3 段（每个标题一段），得到 %d: %#v", len(chunks), chunks)
	}
}

func TestSplitMarkdown_NoHeading(t *testing.T) {
	chunks := splitMarkdown("就一段没有标题的正文\n第二行")
	if len(chunks) != 1 {
		t.Fatalf("无标题应整篇作一条，得到 %d", len(chunks))
	}
}

func TestSplitMarkdown_Empty(t *testing.T) {
	if chunks := splitMarkdown("   \n\n  "); len(chunks) != 0 {
		t.Fatalf("空内容应返回 0 条，得到 %d", len(chunks))
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("# 我的标题\n正文"); got != "我的标题" {
		t.Fatalf("应剥标题符取首行，得到 %q", got)
	}
	if got := firstLine(""); got != "未命名知识" {
		t.Fatalf("空内容应降级，得到 %q", got)
	}
}
