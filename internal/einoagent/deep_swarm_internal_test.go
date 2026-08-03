package einoagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cloudwego/eino/components/tool"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/lead"
)

// TestSubAgentTargetSection 验证子代理固定目标段：host 非空时含目标 + 防 localhost 告诫；空则空串。
func TestSubAgentTargetSection(t *testing.T) {
	got := subAgentTargetSection("111.229.193.40:34280")
	for _, want := range []string{"111.229.193.40:34280", "目标 Host", "127.0.0.1", "localhost"} {
		if !strings.Contains(got, want) {
			t.Errorf("目标段应含 %q, got:\n%s", want, got)
		}
	}

	if s := subAgentTargetSection(""); s != "" {
		t.Errorf("host 空应返回空串, got %q", s)
	}
}

// fakeLeadReader 满足 LeadStore；grouped 由测试设置，readErr 可注入读失败。
type fakeLeadReader struct {
	grouped map[lead.Kind][]lead.Entry
	readErr error
}

func (f *fakeLeadReader) Append(context.Context, string, lead.Entry) error { return nil }
func (f *fakeLeadReader) ReadRecent(context.Context, string) (map[lead.Kind][]lead.Entry, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.grouped, nil
}

// leadSection 是子代理 Instruction 拼装的关键点（§7.5"子代理看不到 orchestrator user message"的解）：
// 验证 recon 写的 lead 确实能被 leadSection 读出并渲染进子代理 system prompt 段。
func TestLeadSection_RendersEntries(t *testing.T) {
	store := &fakeLeadReader{grouped: map[lead.Kind][]lead.Entry{
		lead.KindClue: {{Detail: "/admin/backup 疑似可访问，未验证", SourceTaskID: "t1"}},
	}}
	got := leadSection(context.Background(), store, "target.com")
	if !strings.Contains(got, "/admin/backup 疑似可访问，未验证") {
		t.Fatalf("应包含 lead note: %s", got)
	}
	if !strings.Contains(got, "情报黑板") {
		t.Fatalf("应含情报黑板标题段: %s", got)
	}
}

func TestLeadSection_EmptyWhenNoStoreOrHost(t *testing.T) {
	if got := leadSection(context.Background(), nil, "target.com"); got != "" {
		t.Fatalf("store nil 应返回空串: %q", got)
	}
	store := &fakeLeadReader{grouped: map[lead.Kind][]lead.Entry{lead.KindFact: {{Detail: "x"}}}}
	if got := leadSection(context.Background(), store, ""); got != "" {
		t.Fatalf("host 空应返回空串: %q", got)
	}
}

func TestLeadSection_EmptyOnReadErrorOrNoEntries(t *testing.T) {
	errStore := &fakeLeadReader{readErr: errors.New("redis down")}
	if got := leadSection(context.Background(), errStore, "target.com"); got != "" {
		t.Fatalf("读失败应降级空串（不阻塞子代理装配）: %q", got)
	}
	emptyStore := &fakeLeadReader{grouped: map[lead.Kind][]lead.Entry{}}
	if got := leadSection(context.Background(), emptyStore, "target.com"); got != "" {
		t.Fatalf("无情报应返回空串: %q", got)
	}
}

// TestReconWriteLead_ExploitationReadsInSection 端到端验证 §7.5 核心场景（recon 写 lead →
// exploitation 在 task description/Instruction 里读到）：用真实 *lead.Store（miniredis）+
// 生产装配路径 BuildHunterTools 建 write_lead 工具、recon 猎手调用写入，再用 leadSection
// （BuildDeepSwarm 拼子代理 Instruction 时调用的同一函数）验证 exploitation 能读到。
func TestReconWriteLead_ExploitationReadsInSection(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := lead.NewStore(rdb, "test", time.Hour)

	host := "target.com"
	reconTools, err := BuildHunterTools(HunterDef{ID: "reconnaissance", Tools: []string{"write_lead"}}, ToolBuildCtx{
		Deps:   TrafficAnalysisToolDeps{Lead: store},
		Params: TrafficAnalysisToolParams{TaskID: "t1", HunterID: "h-recon", Host: host},
	})
	if err != nil {
		t.Fatalf("BuildHunterTools(reconnaissance): %v", err)
	}
	if len(reconTools) != 1 {
		t.Fatalf("应恰好装出 1 个 write_lead 工具，got %d", len(reconTools))
	}
	it, ok := reconTools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("write_lead 不是 InvokableTool")
	}
	if _, err := it.InvokableRun(context.Background(), `{"kind":"clue","detail":"/admin/backup 疑似可访问，未验证"}`); err != nil {
		t.Fatalf("recon 调 write_lead 失败: %v", err)
	}

	// exploitation 子代理装配时，BuildDeepSwarm 用同一 leadSection 拼 Instruction。
	got := leadSection(context.Background(), store, host)
	if !strings.Contains(got, "/admin/backup 疑似可访问，未验证") {
		t.Fatalf("exploitation 的 Instruction 段应含 recon 写的 lead，got:\n%s", got)
	}
}
