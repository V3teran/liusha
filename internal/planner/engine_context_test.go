package planner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// contextObservation 构造一条观察：status 非空即假设（证伪时带原因），为空即事实。
func contextObservation(id, category, statement string, status *explorationgraph.VerificationStatus, refuteReason string, offset time.Duration) explorationgraph.Node {
	content, _ := json.Marshal(map[string]string{"category": category, "statement": statement})
	n := explorationgraph.Node{
		ID: id, TaskID: revisionTask, Kind: core.KindObservation, Content: content,
		VerificationStatus: status, SourceType: explorationgraph.SourceExecutor,
		CreatedAt: time.Unix(1700000000, 0).Add(offset),
	}
	if refuteReason != "" {
		n.Metadata, _ = json.Marshal(map[string]string{"refute_reason": refuteReason})
	}
	return n
}

func verification(s explorationgraph.VerificationStatus) *explorationgraph.VerificationStatus {
	return &s
}

// 已证伪的假设无论因何出圈（评估者证伪、机器护栏、验证重试耗尽、评估者不可用）都进规划
// 视野，并带上原因：规划器据原因决定换配方、原方向重提还是放弃。待验证与已坐实的假设不在其列。
func TestGatherContext_RefutedHypothesesCarryEveryReason(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	refuted := verification(explorationgraph.VerificationRefuted)
	reasons := []string{
		evaluator.RefuteClaimInvalid, evaluator.RefuteNotReproducible, evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteInvalidRecipe, evaluator.RefuteJudgeUnavailable,
	}
	nodes := []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		contextObservation("pending", "hypothesis", "待验证", verification(explorationgraph.VerificationPending), "", time.Second),
		contextObservation("confirmed", "hypothesis", "已坐实", verification(explorationgraph.VerificationConfirmed), "", time.Second),
	}
	for i, r := range reasons {
		nodes = append(nodes, contextObservation("h-"+r, "hypothesis", "假设-"+r, refuted, r, time.Duration(i+2)*time.Second))
	}
	for _, n := range nodes {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)

	got := map[string]string{}
	for _, h := range planCtx.RefutedHypotheses {
		got[h.Statement] = h.Reason
	}
	want := map[string]string{}
	for _, r := range reasons {
		want["假设-"+r] = r
	}
	assert.Equal(t, want, got)
	assert.Empty(t, planCtx.Facts, "假设不是事实")
}

// 评估者的证伪理由随假设进规划视野：原因只是类别，缺了什么证据、哪部分已被证明要看理由。
func TestGatherContext_RefutedHypothesisCarriesDetail(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	h := contextObservation("h", "hypothesis", "接口可读取任意记录", verification(explorationgraph.VerificationRefuted), "", time.Second)
	h.Metadata, _ = json.Marshal(map[string]string{"refute_reason": evaluator.RefuteNotReproducible, "refute_detail": "只读到本人记录"})
	for _, n := range []explorationgraph.Node{revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0), h} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)
	require.Len(t, planCtx.RefutedHypotheses, 1)
	assert.Equal(t, "只读到本人记录", planCtx.RefutedHypotheses[0].Detail)
}

// 事实按类别分组呈现给规划器（有什么 / 怎么进入 / 怎么连接 / 怎么反应 / 其他），同类别内
// 近者在前；规划器据此先看清环境的各个侧面，而不是读一串混杂的陈述。
func TestGatherContext_FactsGroupedByCategory(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		contextObservation("f1", "behavior", "搜索参数原样回显", nil, "", 1*time.Second),
		contextObservation("f2", "entity", "目标为 nginx 1.18", nil, "", 2*time.Second),
		contextObservation("f3", "note", "执行总结：完成", nil, "", 3*time.Second),
		contextObservation("f4", "access", "登录接口 /login 用表单认证", nil, "", 4*time.Second),
		contextObservation("f5", "entity", "存在 /admin 页面", nil, "", 5*time.Second),
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)
	section := buildFactsSection(planCtx.Facts)

	order := []string{"存在 /admin 页面", "目标为 nginx 1.18", "登录接口 /login 用表单认证", "搜索参数原样回显", "执行总结：完成"}
	last := -1
	for _, s := range order {
		i := strings.Index(section, s)
		require.GreaterOrEqual(t, i, 0, "缺少事实 %q：\n%s", s, section)
		assert.Greater(t, i, last, "事实 %q 次序不对：\n%s", s, section)
		last = i
	}
	for _, heading := range []string{"有什么", "怎么进入", "怎么反应", "其他"} {
		assert.Contains(t, section, heading)
	}
	assert.NotContains(t, section, "怎么连接", "没有该类别的事实就不出现该类别标题")
}
