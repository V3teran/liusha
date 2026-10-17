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
	Save(ctx context.Context, f VulnFinding) (VulnFinding, error)
}

// ClaimSink 把通用裁决门坐实的主张落成本域交付物（finding 表）。
// 领域评级与定位的全部映射在此：severity 缺省、host 从证据 URL 抽取、
// 交付载荷组装——通用裁决门只透传载荷，不解释领域字段。
//
// 依赖方向：领域交付包认识通用契约（evaluator.ConfirmedClaim），
// 通用门对领域零感知。
type ClaimSink struct {
	store      findingStore
	agentRunID string // 归属 agent_run.id（表 NOT NULL 外键）
}

// NewClaimSink 构造交付管道。
func NewClaimSink(store findingStore, agentRunID string) *ClaimSink {
	return &ClaimSink{store: store, agentRunID: agentRunID}
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
	_, err = s.store.Save(ctx, VulnFinding{
		TaskID:     c.TaskID,
		AgentRunID: &agentRunID,
		Host:       host,
		Summary:    summary,
		Severity:   severity,
		Evaluation: evaluation,
		Target:     c.Target,
		Repro:      c.Repro,
	})
	return err
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
