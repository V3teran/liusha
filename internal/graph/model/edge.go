package model

import (
	"time"

	"github.com/google/uuid"
)

type EdgeKind string

const (
	EdgeFinding   EdgeKind = "finding"
	EdgeChainsTo  EdgeKind = "chains_to"
	EdgeEvidences EdgeKind = "evidences"
	EdgeEnables   EdgeKind = "enables"
)

type Edge struct {
	ID           uuid.UUID
	EngagementID uuid.UUID
	SrcID        uuid.UUID
	DstID        uuid.UUID
	Kind         EdgeKind
	CreatedAt    time.Time
}
