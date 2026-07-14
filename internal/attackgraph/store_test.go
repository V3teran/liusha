package attackgraph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

type fakeMessages struct {
	msgs  []conversation.Message // 按 Seq 升序
	err   error
	calls int
}

func (f *fakeMessages) ListMessages(_ context.Context, _ string, afterSeq int64, limit int) ([]conversation.Message, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []conversation.Message
	for _, m := range f.msgs {
		if m.Seq > afterSeq {
			out = append(out, m)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type fakeFindings struct {
	findings []finding.VulnFinding
	err      error
}

func (f *fakeFindings) ListByTask(_ context.Context, _ string) ([]finding.VulnFinding, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.findings, nil
}

func TestProjectorProject(t *testing.T) {
	ctx := context.Background()

	t.Run("翻页拉全部消息", func(t *testing.T) {
		var msgs []conversation.Message
		n := messagePageSize + 3 // 跨两页，验证翻页循环拉尽
		for i := 0; i < n; i++ {
			m := mkEventMsg(fmt.Sprintf("m%d", i), evReasoning, map[string]any{"Text": "想"})
			m.Seq = int64(i + 1)
			msgs = append(msgs, m)
		}
		p := &Projector{Messages: &fakeMessages{msgs: msgs}, Findings: &fakeFindings{}}

		g, err := p.Project(ctx, "conv-1", "task-1")
		if err != nil {
			t.Fatal(err)
		}
		reasoning := 0
		for _, nd := range g.Nodes {
			if nd.Kind == KindReasoning {
				reasoning++
			}
		}
		if reasoning != n {
			t.Errorf("想节点=%d，期望 %d（翻页未拉全）", reasoning, n)
		}
	})

	t.Run("空 convID 不拉消息只拉漏洞", func(t *testing.T) {
		fm := &fakeMessages{}
		p := &Projector{
			Messages: fm,
			Findings: &fakeFindings{findings: []finding.VulnFinding{mkFinding("a", "h", "high", "x")}},
		}
		g, err := p.Project(ctx, "", "task-o")
		if err != nil {
			t.Fatal(err)
		}
		if fm.calls != 0 {
			t.Errorf("空 convID 不该调 ListMessages，调了 %d 次", fm.calls)
		}
		if len(g.Nodes) != 1 {
			t.Errorf("期望 1 漏洞节点，得 %d", len(g.Nodes))
		}
	})

	t.Run("漏洞读取错误透传", func(t *testing.T) {
		p := &Projector{Messages: &fakeMessages{}, Findings: &fakeFindings{err: errors.New("db down")}}
		if _, err := p.Project(ctx, "", "task-o"); err == nil {
			t.Fatal("期望错误透传")
		}
	})

	t.Run("消息读取错误透传", func(t *testing.T) {
		p := &Projector{Messages: &fakeMessages{err: errors.New("boom")}, Findings: &fakeFindings{}}
		if _, err := p.Project(ctx, "conv-1", "task-o"); err == nil {
			t.Fatal("期望错误透传")
		}
	})
}
