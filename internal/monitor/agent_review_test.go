package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 复核请求在被消费前合并为一个信号，各来源的理由逐条保留（相同理由只留一条）。
func TestRequestReview_CoalescesSignalKeepsEveryReason(t *testing.T) {
	a := &Agent{reviewCh: make(chan struct{}, 1)}

	a.RequestReview("任务目标已调整")
	a.RequestReview("任务目标已调整")
	a.RequestReview("用户指派了动作")

	select {
	case <-a.reviewCh:
	default:
		t.Fatal("请求后应有一个待处理的复核信号")
	}
	select {
	case <-a.reviewCh:
		t.Fatal("多次请求应合并为一个信号")
	default:
	}
	assert.Equal(t, []string{"任务目标已调整", "用户指派了动作"}, a.takeReviewReasons())
	assert.Empty(t, a.takeReviewReasons())
}

// 复核的理由进评估目标：监察者知道为什么提前评估、要看什么。
func TestEvaluationObjective_IncludesReviewReasons(t *testing.T) {
	a := &Agent{taskID: "t1"}
	assert.NotContains(t, a.buildEvaluationObjective(nil), "本轮是复核")
	objective := a.buildEvaluationObjective([]string{"任务目标已被用户调整"})
	assert.Contains(t, objective, "本轮是复核")
	assert.Contains(t, objective, "任务目标已被用户调整")
}
