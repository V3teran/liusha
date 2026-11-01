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
	// onSaved 是落库成功后的观察者（会话时间线的漏洞卡事件源）；
	// nil=无观察者。观察者失败不影响交付本体。
	onSaved func(ctx context.Context, f Finding)
}

// NewClaimSink 构造交付管道。
func NewClaimSink(store findingStore, agentRunID string) *ClaimSink {
	return &ClaimSink{store: store, agentRunID: agentRunID}
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
	severity := c.Severity
	if severity == "" {
		severity = "medium"
	}
	host := c.HostHint
	if host == "" || host == "unknown" {
		host = deriveHost(c.Evidence, c.Repro, c.Target)
	}

	evaluation, err := json.Marshal(map[string]interface{}{
		"node_id":           c.ResultNodeID,
		"verification_id":   c.VerificationID,
		"verification_tier": c.Tier,
		"evaluation":        c.Evidence,
		"duration_ms":       c.DurationMs,
	})
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}

	agentRunID := s.agentRunID
	saved, err := s.store.Save(ctx, Finding{
		TaskID:      c.TaskID,
		AgentRunID:  &agentRunID,
		Host:        host,
		Summary:     summary,
		Severity:    severity,
		Remediation: c.Remediation, // 修复建议随主张坐实落台账（空串由 store NULLIF 归 NULL）
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
