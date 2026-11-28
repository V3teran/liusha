package evaluator

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 判定输入列出本任务已坐实的结果：一行一条，短号在行首，指纹规范键跟在陈述之后；按创建先后
// 编号；陈述与指纹都没有的结果无从对照，不列；没有结果时输入里没有这一节。
func TestBuildObjective_ListsConfirmedResults(t *testing.T) {
	j := NewRouterJudge(nil, zerolog.Nop())
	base := JudgeInput{
		Hypothesis: "H",
		Recipe:     json.RawMessage(`{"kind":"steps"}`),
		Evidence:   json.RawMessage(`{}`),
	}
	if plain := j.buildObjective(base); strings.Contains(plain, "已坐实的结果") {
		t.Fatalf("没有既有结果时不应有这一节:\n%s", plain)
	}

	in := base
	in.Confirmed = []ConfirmedResult{
		{NodeID: "res-1", Statement: "Items 接口的 id 参数存在注入，可读出全表数据", Fingerprint: "sql_injection|get http://t.local/items|id"},
		{NodeID: "res-2", Statement: "备份目录可匿名下载"},
	}
	got := j.buildObjective(in)
	want := "## 机器预跑证据\n{}\n\n## 本任务已坐实的结果\n" +
		"- [R1] Items 接口的 id 参数存在注入，可读出全表数据（sql_injection|get http://t.local/items|id）\n" +
		"- [R2] 备份目录可匿名下载\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("判定输入应以既有结果一节收尾:\n%s", got)
	}
}

// 既有结果清单由任务的结果节点整理而来：陈述钳长、指纹取节点上的规范键、超出上限时留最新的。
func TestConfirmedResults_FromResultNodes(t *testing.T) {
	nodes := []explorationgraph.Node{
		{ID: "res-empty", TaskID: "t1", Kind: core.KindResult},
		{ID: "res-1", TaskID: "t1", Kind: core.KindResult,
			Content:  json.RawMessage(`{"statement":"` + strings.Repeat("长", confirmedStatementClampRunes+20) + `"}`),
			Metadata: json.RawMessage(`{"claim_fingerprint":"weak_credential|post http://t.local/login|username","claim_class":"weak_credential"}`)},
		{ID: "res-2", TaskID: "t1", Kind: core.KindResult, Content: json.RawMessage(`{"statement":"备份目录可匿名下载"}`)},
	}
	list := confirmedResults(nodes)
	if len(list) != 2 {
		t.Fatalf("陈述与指纹都没有的结果不列: %+v", list)
	}
	if want := "…（截断）"; !strings.HasSuffix(list[0].Statement, want) || len([]rune(list[0].Statement)) != confirmedStatementClampRunes+len([]rune(want)) {
		t.Fatalf("陈述应钳到 %d 字符: %q", confirmedStatementClampRunes, list[0].Statement)
	}
	if list[0].NodeID != "res-1" || list[0].Fingerprint != "weak_credential|post http://t.local/login|username" {
		t.Fatalf("第一条不符: %+v", list[0])
	}
	if list[1] != (ConfirmedResult{NodeID: "res-2", Statement: "备份目录可匿名下载"}) {
		t.Fatalf("第二条不符: %+v", list[1])
	}

	many := make([]explorationgraph.Node, 0, maxConfirmedResults+5)
	for i := range maxConfirmedResults + 5 {
		many = append(many, explorationgraph.Node{ID: "res-" + string(rune('a'+i)), Kind: core.KindResult,
			Content: json.RawMessage(`{"statement":"s"}`)})
	}
	kept := confirmedResults(many)
	if len(kept) != maxConfirmedResults || kept[0].NodeID != many[5].ID || kept[len(kept)-1].NodeID != many[len(many)-1].ID {
		t.Fatalf("超出上限应只留最新的 %d 条: %d 条，首条 %s", maxConfirmedResults, len(kept), kept[0].NodeID)
	}
}

// 清单有字节预算：陈述都很长时字节上限先于条数上限到顶，同样只留最新的——它不会把判定的
// 首条输入顶过单条消息的硬上限。
func TestConfirmedResults_FitByteBudget(t *testing.T) {
	nodes := make([]explorationgraph.Node, 0, 2*maxConfirmedResults)
	statement := strings.Repeat("长", confirmedStatementClampRunes)
	fingerprint := strings.Repeat("k", 30)
	for i := range 2 * maxConfirmedResults {
		nodes = append(nodes, explorationgraph.Node{ID: "res-" + strconv.Itoa(i), Kind: core.KindResult,
			Content:  json.RawMessage(`{"statement":"` + statement + `"}`),
			Metadata: json.RawMessage(`{"claim_fingerprint":"` + fingerprint + `"}`)})
	}
	kept := confirmedResults(nodes)
	if len(kept) == 0 || len(kept) >= maxConfirmedResults {
		t.Fatalf("字节上限应先于条数上限到顶: %d 条", len(kept))
	}
	if kept[0].NodeID != nodes[len(nodes)-len(kept)].ID || kept[len(kept)-1].NodeID != nodes[len(nodes)-1].ID {
		t.Fatalf("应留最新的 %d 条，首条却是 %s", len(kept), kept[0].NodeID)
	}
	if size := len(renderConfirmedResults(kept)); size > maxConfirmedResultsBytes+64 {
		t.Fatalf("渲染结果 %d 字节，超出预算 %d", size, maxConfirmedResultsBytes)
	}
}

// 判定里的 duplicate_of 是既有结果的短号：坐实的判定把它落成结果节点 ID；短号不在清单里
// （失效引用）或证伪的判定带上它，都按独立主张处理，判定本身不受影响。前缀不分大小写。
func TestSettleDuplicate(t *testing.T) {
	confirmed := []ConfirmedResult{{NodeID: "res-1"}, {NodeID: "res-2"}}
	for _, tc := range []struct {
		name    string
		v       Verdict
		wantDup string
	}{
		{"坐实指明再次确认", Verdict{Verdict: VerdictConfirmed, DuplicateOf: "R2"}, "res-2"},
		{"短号前缀不分大小写", Verdict{Verdict: VerdictConfirmed, DuplicateOf: " r1 "}, "res-1"},
		{"独立主张", Verdict{Verdict: VerdictConfirmed}, ""},
		{"短号不在清单里", Verdict{Verdict: VerdictConfirmed, DuplicateOf: "R9"}, ""},
		{"写的不是短号", Verdict{Verdict: VerdictConfirmed, DuplicateOf: "res-1"}, ""},
		{"证伪的判定带上它", Verdict{Verdict: VerdictRefuted, DuplicateOf: "R1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := settleDuplicate(tc.v, confirmed, zerolog.Nop()); got.DuplicateOf != tc.wantDup {
				t.Fatalf("DuplicateOf = %q，期望 %q", got.DuplicateOf, tc.wantDup)
			}
		})
	}
}

// 判定 JSON 的 duplicate_of 原样带出，落定是判定之后的事。
func TestParseVerdict_DuplicateOf(t *testing.T) {
	v, err := parseVerdict(`{"verdict":"confirmed","confidence":0.9,"duplicate_of":"R2"}`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if v.DuplicateOf != "R2" {
		t.Fatalf("duplicate_of 应原样带出，得 %q", v.DuplicateOf)
	}
}

// 时间线上叙述得出再次确认：结论仍是在线坐实，但说明它折叠成了既有结果的佐证。
func TestVerdictNarrative_NotesDuplicate(t *testing.T) {
	folded := verdictNarrative(Verdict{Verdict: VerdictConfirmed, Confidence: 0.9, DuplicateOf: "res-2", Reasoning: "r"})
	if !strings.HasPrefix(folded, "评估结论：坐实") || !strings.Contains(folded, "既有结果的再次确认") {
		t.Fatalf("再次确认的判定应说明折叠: %q", folded)
	}
	if plain := verdictNarrative(Verdict{Verdict: VerdictConfirmed, Confidence: 0.9, Reasoning: "r"}); strings.Contains(plain, "再次确认") {
		t.Fatalf("独立主张不该提再次确认: %q", plain)
	}
}
