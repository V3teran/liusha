package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// relationsLister 是 ReadRelations 工具依赖的最小读接口，由 *finding.Store 自动满足。
type relationsLister interface {
	ListRelationsByEngagement(ctx context.Context, engagementID string) ([]finding.Relation, error)
}

// ReadRelations — 列出本 engagement 内所有 finding 之间的 enables 边（图拓扑；与 write_relation 配对）。
type ReadRelations struct {
	Store        relationsLister
	OwnerID string // builder 注入；空时 Execute 报错
}

// Name 返回工具名 "read_relations"。
func (a *ReadRelations) Name() string { return "read_relations" }

func (a *ReadRelations) Description() string {
	return "列出本 engagement 内所有 finding 之间的 enables 边（write_relation 写入的图拓扑）。" +
		"**何时用**：推理组合漏洞时想看哪些 finding 已声明依赖、哪些孤立；" +
		"或写新 enables 边前查重避免冗余。返回 [{from, to, reason, created_at}]。"
}

// ParametersJSON 无入参（owner_id 由 builder 注入）。
func (a *ReadRelations) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// relationItem 是给 LLM 看的瘦摘要项。
type relationItem struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Execute 列出 engagement 全部 enables 关系。
func (a *ReadRelations) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil {
		return toolfx.Result{}, errors.New("read_relations: Store nil")
	}
	if a.OwnerID == "" {
		return toolfx.Result{}, errors.New("read_relations: OwnerID 必填（builder 注入失败）")
	}

	rs, err := a.Store.ListRelationsByEngagement(ctx, a.OwnerID)
	if err != nil {
		return toolfx.Result{}, err
	}

	items := make([]relationItem, 0, len(rs))
	for _, r := range rs {
		var reason string
		if len(r.Payload) > 0 {
			var p struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(r.Payload, &p)
			reason = p.Reason
		}
		items = append(items, relationItem{
			From:      r.FromFindingID,
			To:        r.ToFindingID,
			Reason:    reason,
			CreatedAt: r.CreatedAt,
		})
	}
	output, err := json.Marshal(map[string]any{
		"count":     len(items),
		"relations": items,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal relations: %w", err)
	}
	return toolfx.Result{Output: output}, nil
}
