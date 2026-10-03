package eval

import (
	"context"
	"errors"
	"testing"
)

func TestRuleEvaluatorSuccess(t *testing.T) {
	e := NewRuleEvaluator(Thresholds{})
	res, err := e.Evaluate(context.Background(), TraceInput{
		Objective: "x", Iterations: 5, Actions: 10, Failures: 1, Status: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("成功轨迹应通过: %+v", res)
	}
	if res.Details["status"] != 1 {
		t.Fatalf("status 维度应为 1")
	}
}

func TestRuleEvaluatorErrorStatus(t *testing.T) {
	e := NewRuleEvaluator(Thresholds{})
	res, _ := e.Evaluate(context.Background(), TraceInput{Status: "error", Iterations: 3, Actions: 2, Failures: 0})
	if res.Passed {
		t.Fatalf("error 终止不应通过")
	}
}

func TestRuleEvaluatorLowToolRate(t *testing.T) {
	e := NewRuleEvaluator(Thresholds{})
	res, _ := e.Evaluate(context.Background(), TraceInput{Status: "success", Iterations: 5, Actions: 10, Failures: 8})
	if res.Details["tools"] >= 0.5 {
		t.Fatalf("20%% 成功率不该拿到 0.5+")
	}
}

type stubJudge struct {
	content string
	err     error
}

func (s stubJudge) Complete(_ context.Context, _ JudgeRequest) (JudgeResponse, error) {
	return JudgeResponse{Content: s.content}, s.err
}

func TestLLMJudgeParse(t *testing.T) {
	j := NewLLMJudge(stubJudge{content: "噪声 {\"score\": 0.9, \"passed\": true, \"reason\": \"达成\"} 尾部"})
	res, err := j.Evaluate(context.Background(), TraceInput{Objective: "o", Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Score != 0.9 || !res.Passed {
		t.Fatalf("解析错误: %+v", res)
	}
}

func TestLLMJudgeBadJSON(t *testing.T) {
	j := NewLLMJudge(stubJudge{content: "no json"})
	if _, err := j.Evaluate(context.Background(), TraceInput{}); err == nil {
		t.Fatal("应报错")
	}
}

func TestLLMJudgeProviderError(t *testing.T) {
	j := NewLLMJudge(stubJudge{err: errors.New("boom")})
	if _, err := j.Evaluate(context.Background(), TraceInput{}); err == nil {
		t.Fatal("应报错")
	}
}

func TestCompositeWeighted(t *testing.T) {
	c, err := NewComposite(
		[]Evaluator{NewRuleEvaluator(Thresholds{}), NewLLMJudge(stubJudge{content: `{"score":1.0,"passed":true,"reason":"ok"}`})},
		[]float64{0.5, 0.5},
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Evaluate(context.Background(), TraceInput{Status: "success", Iterations: 5, Actions: 10, Failures: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Details["rule"]; !ok {
		t.Fatalf("缺 rule 维度")
	}
	if _, ok := res.Details["llm_judge"]; !ok {
		t.Fatalf("缺 llm_judge 维度")
	}
}

func TestCompositeBadArgs(t *testing.T) {
	if _, err := NewComposite(nil, nil); err == nil {
		t.Fatal("应报错")
	}
}
