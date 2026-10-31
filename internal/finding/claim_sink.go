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
		host = hostFromEvidence(c.Evidence)
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

// hostFromEvidence 从复现证据的 url 字段抽主机（抽不出回 unknown）。
func hostFromEvidence(ev json.RawMessage) string {
	var e struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(ev, &e) == nil && e.URL != "" {
		if u, pErr := url.Parse(e.URL); pErr == nil && u.Host != "" {
			return u.Host
		}
	}
	return "unknown"
}
