package model

import (
	"time"

	"github.com/google/uuid"
)

type NodeKind string

const (
	KindSurface      NodeKind = "surface"
	KindHypothesis   NodeKind = "hypothesis"
	KindFinding      NodeKind = "finding"
	KindExploitChain NodeKind = "exploit_chain"
	KindEvidence     NodeKind = "evidence"
)

type Node struct {
	ID             uuid.UUID
	EngagementID   uuid.UUID
	Kind           NodeKind
	Payload        map[string]any
	Vector         []float32 // nil 表示尚未嵌入
	EmbeddingModel string
	DedupKey       string
	CreatedAt      time.Time
}
