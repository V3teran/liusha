// Package corpus 是跨目标长期知识库（hybrid RAG）的 Go 模型与持久化层。
//
// 见 spec 2026-07-12-lesson-to-corpus-rag.md。取代旧 lesson 的 global 半：
//   - 无 host 列——corpus 是跨目标可复用知识（host 归 lead）。
//   - hybrid 检索：dense（pgvector HNSW cosine）+ sparse（pg_trgm）两路召回 → Jina rerank → top-k。
//   - 来源二分：agent（自学/收尾蒸馏）| expert（外部 markdown 导入）。
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Source 是知识来源。
const (
	SourceAgent  = "agent"  // agent 自学 / 收尾蒸馏写入
	SourceExpert = "expert" // 专家经验 / 历史报告，离线导入
)

// Entry 是 corpus 表一行的 Go 表示。
//
// Embedding 可空（nil = 未 embed，不进 dense 召回但仍可 sparse 命中）。
// SourceTaskID 仅 agent 来源有值（溯源到哪次 task）；expert 导入为空。
type Entry struct {
	ID           string
	Title        string
	Content      string
	Tags         []string
	Source       string
	SourceTaskID string
	Embedding    []float32
	ContentHash  string
	HitCount     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ContentHash 计算 content 的 SHA-256 hex（去重键）。TrimSpace 归一化，容忍 LLM 输出的前后空白。
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}
