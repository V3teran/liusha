package conversation

import (
	"context"
	"encoding/json"
	"errors"
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

// 可空 uuid/text 列读用 COALESCE 把 NULL 折成空串（Conversation 字段是 string 不接 NULL）。
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

// DeleteConversation 删除对话及其消息（message FK ON DELETE CASCADE 自动连带删）。
// 不动关联的 active_scan / finding（scan_id FK 是 SET NULL，渗透成果以 owner=scan 为根，
// 不因删对话丢失）。id 不存在返回 not found 错误。
func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM conversation WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete conversation %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete conversation %s: not found", id)
	}
	return nil
}

// ResolveOwnerID 解析对话关联的 owner id（用于聚合 llm_invocation / tool_invocation 用量）。
//   - active：conversation.scan_id 即 owner（active_scan id）。
//   - passive：conversation 无 scan_id，反查 passive_session.conversation_id 拿会话 id。
//   - 纯聊天：两者皆无 → 返回空串（调用方据此返回零用量）。
func (s *Store) ResolveOwnerID(ctx context.Context, convID string) (string, error) {
	c, err := s.GetConversation(ctx, convID)
	if err != nil {
		return "", err
	}
	if c.ScanID != "" {
		return c.ScanID, nil
	}
	var oid string
	err = s.pool.QueryRow(ctx,
		`SELECT id::text FROM passive_session WHERE conversation_id=$1 LIMIT 1`, convID).Scan(&oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil // 纯聊天，无关联 owner
	}
	if err != nil {
		return "", fmt.Errorf("resolve passive owner for conversation %s: %w", convID, err)
	}
	return oid, nil
}

// IsRunActive 返回本对话当前是否有正在运行的扫描（权威：后端 owner 终态，非客户端计时）。
//   - active：关联 active_scan.status='active'（completed/aborted 为终态）。
//   - passive：passive_session.status='active'。
//   - 纯聊天 / 已结束：false。
//
// 前端据此显示"agent 工作中"指示器，避免历史回灌误判（双参传 convID 规避 uuid/text 参数歧义）。
func (s *Store) IsRunActive(ctx context.Context, convID string) (bool, error) {
	var running bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
		         SELECT 1 FROM active_scan a JOIN conversation c ON c.scan_id = a.id
		         WHERE c.id = $1 AND a.status = 'active'
		       )
		    OR EXISTS(
		         SELECT 1 FROM passive_session p
		         WHERE p.conversation_id = $2 AND p.status = 'active'
		       )`, convID, convID).Scan(&running)
	if err != nil {
		return false, fmt.Errorf("check run active for conversation %s: %w", convID, err)
	}
	return running, nil
}

// WallclockMs 返回本对话关联扫描的「纯工作」墙钟时长（毫秒）——发起→完成的流逝时间扣除停顿。
// 跑中用 now()-created_at，结束用 ended_at-created_at；再减 active_scan.paused_ms（多轮 follow-up
// 复活间的用户停顿累计，见 activescan.Reopen）——避免多轮场景把「完成→追加」的等待算进耗时。
// 首次扫描 paused_ms=0，口径与原墙钟一致。区别于 Σ(LLM latency+工具 duration)（并发累加会更高）。
// 纯聊天/无关联 owner 返回 0。passive_session 无复活、无停顿，不扣。
func (s *Store) WallclockMs(ctx context.Context, convID string) (int64, error) {
	var ms *int64
	err := s.pool.QueryRow(ctx, `
		SELECT ((EXTRACT(EPOCH FROM (COALESCE(a.ended_at, now()) - a.created_at)) * 1000)::bigint - a.paused_ms)
		FROM active_scan a JOIN conversation c ON c.scan_id = a.id
		WHERE c.id = $1
		UNION ALL
		SELECT (EXTRACT(EPOCH FROM (COALESCE(p.ended_at, now()) - p.created_at)) * 1000)::bigint
		FROM passive_session p
		WHERE p.conversation_id = $2
		LIMIT 1`, convID, convID).Scan(&ms)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("wallclock for conversation %s: %w", convID, err)
	}
	if ms == nil {
		return 0, nil
	}
	return *ms, nil
}

// ListConversations 按 updated_at DESC 列出最近活跃的对话（UI 列表）。
func (s *Store) ListConversations(ctx context.Context, limit int) ([]Conversation, error) {
	limit = clampLimit(limit)
	// run_status：派生「真实运行态」——conversation.status 是僵尸字段（默认 active 从不更新），
	// 不能用于显示。取关联 active_scan.status（active/completed/aborted），无 scan 则反查
	// passive_session.status，都无（纯聊天）→ 空串。前端列表据此显示准确状态。
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, COALESCE(c.title,''), COALESCE(c.scan_id::text,''),
			COALESCE(c.role_id,''), c.status, c.created_at, c.updated_at,
			COALESCE(a.status, p.status, '') AS run_status
		FROM conversation c
		LEFT JOIN active_scan a ON a.id = c.scan_id
		LEFT JOIN passive_session p ON p.conversation_id = c.id::text
		ORDER BY c.updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := scanConversationWithRun(rows, &c); err != nil {
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
	// touch updated_at（仅真实对话消息 user/assistant）：失败仅影响列表排序，消息已落库——不报错。
	// KindEvent 是高频 agent 过程事件（active 一次几百条），不参与会话列表排序，跳过这次 DB 往返
	// ——省掉热路径上每事件的第二次同步写。
	if kind != KindEvent {
		_, _ = s.pool.Exec(ctx, "UPDATE conversation SET updated_at=now() WHERE id=$1", convID)
	}
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

// ListRecentDialog 取对话内最近 limit 条「普通对话消息」（KindMessage，即 user/assistant/system；
// 滤掉高频 agent 过程事件 KindEvent），按 seq 升序返回。供 agent 读对话历史当工作上下文
// （阶段0：active 多轮追问连贯性）。limit ≤ 0 用默认。
func (s *Store) ListRecentDialog(ctx context.Context, convID string, limit int) ([]Message, error) {
	limit = clampLimit(limit)
	// 先按 seq DESC 取最近 limit 条，再在 Go 里反转成升序（对话历史按时间正序喂 LLM）。
	rows, err := s.pool.Query(ctx,
		"SELECT "+msgCols+" FROM message WHERE conversation_id=$1 AND kind=$2 ORDER BY seq DESC LIMIT $3",
		convID, KindMessage, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent dialog of %s: %w", convID, err)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i] // DESC → 升序
	}
	return out, nil
}

// scanRow 是 pgx.Row / pgx.Rows 共用的 Scan 抽象（QueryRow 与 Query 迭代复用同一 scan 逻辑）。
type scanRow interface {
	Scan(dest ...any) error
}

func scanConversation(r scanRow, c *Conversation) error {
	return r.Scan(&c.ID, &c.Title, &c.ScanID, &c.RoleID, &c.Status, &c.CreatedAt, &c.UpdatedAt)
}

// scanConversationWithRun 多扫一列 run_status（派生真实运行态，见 ListConversations）。
func scanConversationWithRun(r scanRow, c *Conversation) error {
	return r.Scan(&c.ID, &c.Title, &c.ScanID, &c.RoleID, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.RunStatus)
}

// RunStatus 返回对话关联扫描的「真实运行态」（active_scan.status：active/completed/aborted；
// passive_session.status；纯聊天空串）——供顶部状态栏显示三态，区别于 IsRunActive 的二元布尔。
// 用 conversation.status 是僵尸字段（默认 active 从不更新），不可用于显示。
func (s *Store) RunStatus(ctx context.Context, convID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(a.status, p.status, '')
		FROM conversation c
		LEFT JOIN active_scan a ON a.id = c.scan_id
		LEFT JOIN passive_session p ON p.conversation_id = c.id::text
		WHERE c.id = $1`, convID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("run status for conversation %s: %w", convID, err)
	}
	return status, nil
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
