package conversation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 conversation + message 两表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const (
	defaultListLimit = 50
	maxListLimit     = 500
)

// 可空 uuid/text 列读用 COALESCE 折 NULL→”（Conversation 字段是 string 不接 NULL）。
const convCols = "id, COALESCE(title,''), COALESCE(scan_id::text,''), " +
	"COALESCE(role_id,''), status, created_at, updated_at"

// CreateConversation 建一个对话会话。title/scanID/roleID 为空时存 NULL。
func (s *Store) CreateConversation(ctx context.Context, title, scanID, roleID string) (Conversation, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO conversation (title, scan_id, role_id)
		VALUES (NULLIF($1,''), NULLIF($2,'')::uuid, NULLIF($3,''))
		RETURNING `+convCols, title, scanID, roleID)
	var c Conversation
	if err := scanConversation(row, &c); err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return c, nil
}

// GetConversation 按主键读对话（不限 status）。
func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+convCols+" FROM conversation WHERE id=$1", id)
	var c Conversation
	if err := scanConversation(row, &c); err != nil {
		return Conversation{}, fmt.Errorf("get conversation %s: %w", id, err)
	}
	return c, nil
}

// ListConversations 按 updated_at DESC 列出最近活跃的对话（UI 列表）。
func (s *Store) ListConversations(ctx context.Context, limit int) ([]Conversation, error) {
	limit = clampLimit(limit)
	rows, err := s.pool.Query(ctx,
		"SELECT "+convCols+" FROM conversation ORDER BY updated_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := scanConversation(rows, &c); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetTitle 回填对话标题（首条消息摘要）。空 title 存 NULL。
func (s *Store) SetTitle(ctx context.Context, id, title string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE conversation SET title=NULLIF($1,''), updated_at=now() WHERE id=$2", title, id)
	if err != nil {
		return fmt.Errorf("set conversation %s title: %w", id, err)
	}
	return nil
}

// LinkScan 把对话关联到一次 active_scan（对话发起扫描后回填）。
func (s *Store) LinkScan(ctx context.Context, convID, scanID string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE conversation SET scan_id=NULLIF($1,'')::uuid, updated_at=now() WHERE id=$2", scanID, convID)
	if err != nil {
		return fmt.Errorf("link conversation %s scan: %w", convID, err)
	}
	return nil
}

const msgCols = "seq, id, conversation_id, role, kind, content, metadata, created_at"

// AppendMessage 往对话追加一条消息（普通消息或 agent 过程事件），并刷新 conversation.updated_at
// 让对话列表按活跃排序。metadata 可为 nil。
//
// 不开事务：append + touch updated_at 两条 Exec，与项目"保持简单不上事务"一致——
// touch 失败仅影响列表排序，不影响消息已落库。
func (s *Store) AppendMessage(ctx context.Context, convID string, role Role, kind Kind, content string, metadata json.RawMessage) (Message, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO message (conversation_id, role, kind, content, metadata)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+msgCols, convID, role, kind, content, metadata)
	var m Message
	if err := scanMessage(row, &m); err != nil {
		return Message{}, fmt.Errorf("append message to %s: %w", convID, err)
	}
	// touch updated_at：失败仅影响列表排序，消息已落库——不报错。
	_, _ = s.pool.Exec(ctx, "UPDATE conversation SET updated_at=now() WHERE id=$1", convID)
	return m, nil
}

// ListMessages 取对话内 seq>afterSeq 的消息（按 seq 升序）。afterSeq=0 取全部（回看）；
// SSE 重连后传上次 seq 做增量拉取。
func (s *Store) ListMessages(ctx context.Context, convID string, afterSeq int64, limit int) ([]Message, error) {
	limit = clampLimit(limit)
	rows, err := s.pool.Query(ctx,
		"SELECT "+msgCols+" FROM message WHERE conversation_id=$1 AND seq>$2 ORDER BY seq LIMIT $3",
		convID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages of %s: %w", convID, err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		if err := scanMessage(rows, &m); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanRow 是 pgx.Row / pgx.Rows 共用的 Scan 抽象（QueryRow 与 Query 迭代复用同一 scan 逻辑）。
type scanRow interface {
	Scan(dest ...any) error
}

func scanConversation(r scanRow, c *Conversation) error {
	return r.Scan(&c.ID, &c.Title, &c.ScanID, &c.RoleID, &c.Status, &c.CreatedAt, &c.UpdatedAt)
}

func scanMessage(r scanRow, m *Message) error {
	return r.Scan(&m.Seq, &m.ID, &m.ConversationID, &m.Role, &m.Kind, &m.Content, &m.Metadata, &m.CreatedAt)
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// 断言 pgx.Rows 满足 scanRow（编译期校验，迭代路径用）。
var _ scanRow = pgx.Rows(nil)
