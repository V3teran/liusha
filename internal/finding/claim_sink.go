package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/V3teran/liusha/internal/evaluator"
)

// findingStore 是交付存储的最小写接口（*Store 天然满足，测试可替身）。
type findingStore interface {
	Save(ctx context.Context, f Finding) (Finding, error)
}

// ClaimSink 把通用裁决门坐实的主张落成本域交付物（finding 表）。
// 领域评级与定位的全部映射在此：severity 缺省、host 从证据 URL 抽取、
// 交付载荷组装——通用裁决门只透传载荷，不解释领域字段。
//
// 依赖方向：领域交付包认识通用契约（evaluator.ConfirmedClaim），
// 通用门对领域零感知。
type ClaimSink struct {
	store      findingStore
	agentRunID string // 归属 agent_run.id（表 NOT NULL 外键)
	// taskHost 是主机线索全部落空时的最终兜底（任务锚定的目标主机——task 行
	// 的 target_host）。nil = 无兜底，主机缺失的交付按原样被存储校验拒绝。
	taskHost func(ctx context.Context, taskID string) (string, error)
	// onSaved 是落库成功后的观察者（会话时间线的漏洞卡事件源）；
	// nil=无观察者。观察者失败不影响交付本体。
	onSaved func(ctx context.Context, f Finding)
}

// NewClaimSink 构造交付管道。
func NewClaimSink(store findingStore, agentRunID string) *ClaimSink {
	return &ClaimSink{store: store, agentRunID: agentRunID}
}

// WithTaskHostFallback 注入任务级主机兜底（链式）。坐实材料抽不出主机时查
// 任务锚定的目标主机——它是任务的权威指向；缺这层兜底，复现配方文本化后
// URL 抽取全部落空的交付会被存储校验以「finding.Host 必填」整批拒绝。
func (s *ClaimSink) WithTaskHostFallback(fn func(ctx context.Context, taskID string) (string, error)) *ClaimSink {
	s.taskHost = fn
	return s
}

// WithObserver 注入落库成功后的观察者（链式）。
func (s *ClaimSink) WithObserver(fn func(ctx context.Context, f Finding)) *ClaimSink {
	s.onSaved = fn
	return s
}

// Record 落一条交付物。host 优先取主张载荷自带值，缺省从复现证据的
// URL 抽真实主机；severity 缺省 medium（领域评级默认值归交付层）。
func (s *ClaimSink) Record(ctx context.Context, c evaluator.ConfirmedClaim) error {
	if s == nil || s.store == nil {
		return nil
	}

	summary := c.Statement
	if summary == "" {
		summary = fmt.Sprintf("Confirmed claim (node %s)", c.ResultNodeID)
	}
	// 中立→安全域词汇翻译发生在交付边界：priority→severity（缺省
	// medium 归域侧）、suggestion→remediation；impact 同名直传。
	severity := c.Priority
	if severity == "" {
		severity = "medium"
	}
	host := c.HostHint
	if host == "" || host == "unknown" {
		host = deriveHost(c.Evidence, c.Repro, c.Target)
	}
	if host == "" && s.taskHost != nil {
		// 兜底失败按无主机继续——存储校验的拒绝日志即观测点
		if h, err := s.taskHost(ctx, c.TaskID); err == nil && h != "" && h != "unknown" {
			host = h
		}
	}

	// evaluation 列的证据链形态：裁决结论/成色/理由提为第一层键（前端按
	// 键直取渲染），复放证据与机器断言明细收进 evidence 子对象。嵌套倾倒
	// 形态（evaluation.evaluation.*）让前端只能平铺出乱码 JSON 块——
	// 台账是给人看的，结构为消费方设计。
	evidence := map[string]interface{}{}
	if len(c.Evidence) > 0 {
		if err := json.Unmarshal(c.Evidence, &evidence); err != nil {
			evidence = map[string]interface{}{"raw": string(c.Evidence)}
		}
	}
	tier := c.Tier
	if tier == "" {
		tier = "unknown"
	}
	judgeReasoning := ""
	if r, ok := evidence["reasoning"].(string); ok {
		judgeReasoning = r
	}
	evaluation, err := json.Marshal(map[string]interface{}{
		"node_id":           c.ResultNodeID,
		"verification_id":   c.VerificationID,
		"verification_tier": tier,
		"judge_reasoning":   judgeReasoning,
		"impact":            c.Impact,
		"duration_ms":       c.DurationMs,
		"evidence":          evidence,
	})
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}

	// target 缺席时从复现配方派生（method/url/path）——台账的定位列不该
	// 依赖 LLM 是否记得写 target；配方是机器事实，派生永不失真。
	if len(c.Target) == 0 || string(c.Target) == "{}" || string(c.Target) == "null" {
		c.Target = deriveTarget(c.Repro)
	}

	agentRunID := s.agentRunID
	saved, err := s.store.Save(ctx, Finding{
		TaskID:      c.TaskID,
		AgentRunID:  &agentRunID,
		Host:        host,
		Summary:     summary,
		Severity:    severity,
		Remediation: c.Suggestion, // 处理建议随主张坐实落台账（空串由 store NULLIF 归 NULL）
		Evaluation:  evaluation,
		Target:      c.Target,
		Repro:       c.Repro,
	})
	if err != nil {
		return err
	}
	if s.onSaved != nil {
		s.onSaved(ctx, saved)
	}
	return nil
}

// deriveTarget 从复现配方的 request 派生定位四元组 {method,url,path}。
// 配方是坐实主张的机器事实——定位从它派生比信任 LLM 转述更可靠。
func deriveTarget(repro json.RawMessage) json.RawMessage {
	var recipe struct {
		Recipe struct {
			Request struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
		} `json:"recipe"`
	}
	if json.Unmarshal(repro, &recipe) != nil || recipe.Recipe.Request.URL == "" {
		return nil
	}
	u, err := url.Parse(recipe.Recipe.Request.URL)
	if err != nil {
		return nil
	}
	method := recipe.Recipe.Request.Method
	if method == "" {
		method = "GET"
	}
	out, _ := json.Marshal(map[string]string{
		"method": method,
		"url":    recipe.Recipe.Request.URL,
		"path":   u.Path,
	})
	return out
}

// deriveHost 从坐实材料的可用线索抽主机：证据 url → 复现配方首请求 url →
// 目标指向。地址的形态不唯一（host:port 只是最常见的一种），抽不出时落空串
// 而非占位符——空是诚实的「无此维度」，占位符是伪装的数据。
func deriveHost(evidence, repro, target json.RawMessage) string {
	var withURL struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(evidence, &withURL) == nil {
		if h := hostOf(withURL.URL); h != "" {
			return h
		}
	}
	var recipe struct {
		Recipe struct {
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"recipe"`
	}
	if json.Unmarshal(repro, &recipe) == nil {
		if h := hostOf(recipe.Recipe.Request.URL); h != "" {
			return h
		}
	}
	var t struct {
		URL  string `json:"url"`
		Host string `json:"host"`
	}
	if json.Unmarshal(target, &t) == nil {
		if h := hostOf(t.URL); h != "" {
			return h
		}
		if t.Host != "" && t.Host != "unknown" {
			return t.Host
		}
	}
	return ""
}

func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}
