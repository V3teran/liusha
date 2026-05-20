package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RelationKindEnables 是当前唯一支持的 relation 类型：
// finding A 是 finding B 的前提（如：拿到 admin cookie 才能测某 BAC）。
const RelationKindEnables = "enables"

// Relation 是 finding_relation 表行的 Go 表示。
type Relation struct {
	ID            string
	FromFindingID string
	ToFindingID   string
	Kind          string          // 当前仅 "enables"
	Payload       json.RawMessage // 含 {"reason": "..."} 等可选信息
	CreatedAt     time.Time
}

// SaveRelation 写入一条 enables 边到 finding_relation 表（ON CONFLICT 幂等）。
//
// 由 write_relation 工具调用：LLM 显式声明组合漏洞依赖。
// 重复调（同 from/to/kind）静默更新 payload——LLM 多轮推理可能反复声明同一关系。
//
// reason 可空，会被序列化进 payload.reason。
func (s *Store) SaveRelation(ctx context.Context, fromID, toID, reason string) (Relation, error) {
	if fromID == "" || toID == "" {
		return Relation{}, fmt.Errorf("SaveRelation: from/to finding id 必填")
	}
	if fromID == toID {
		return Relation{}, fmt.Errorf("SaveRelation: from 与 to 不能相同（CHECK 约束 finding_relation_no_self）")
	}

	payload := json.RawMessage(`{}`)
	if reason != "" {
		p, _ := json.Marshal(map[string]string{"reason": reason})
		payload = p
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO finding_relation (from_finding_id, to_finding_id, kind, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (from_finding_id, to_finding_id, kind) DO UPDATE
			SET payload = COALESCE(EXCLUDED.payload, finding_relation.payload)
		RETURNING id, from_finding_id, to_finding_id, kind, payload, created_at`,
		fromID, toID, RelationKindEnables, payload)

	var r Relation
	var pl []byte
	if err := row.Scan(&r.ID, &r.FromFindingID, &r.ToFindingID, &r.Kind, &pl, &r.CreatedAt); err != nil {
		return Relation{}, fmt.Errorf("save relation: %w", err)
	}
	r.Payload = pl
	return r, nil
}

// ListRelationsByEngagement 列出本 owner 下所有 finding 之间的 relation。
// 方法名保留向后兼容；参数 ID 是 owner_id。
//
// JOIN finding 表过滤——只返回 from/to 都在本 owner 内的边。
func (s *Store) ListRelationsByEngagement(ctx context.Context, engagementID string) ([]Relation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.from_finding_id, r.to_finding_id, r.kind, r.payload, r.created_at
		FROM finding_relation r
		JOIN finding f1 ON f1.id = r.from_finding_id
		JOIN finding f2 ON f2.id = r.to_finding_id
		WHERE f1.owner_id=$1::uuid AND f2.owner_id=$1::uuid
		ORDER BY r.created_at`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	defer rows.Close()

	var out []Relation
	for rows.Next() {
		var r Relation
		var pl []byte
		if err := rows.Scan(&r.ID, &r.FromFindingID, &r.ToFindingID, &r.Kind, &pl, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		r.Payload = pl
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relations: %w", err)
	}
	return out, nil
}
