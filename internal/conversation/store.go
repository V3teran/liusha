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
// 不含 status 列：conversation.status 是僵尸字段（已退役），运行态一律派生（见 RunStatus）。
const convCols = "id, COALESCE(title,''), COALESCE(task_id::text,''), " +
	"created_at, updated_at"

// CreateConversation 建一个会话。title/taskID 为空时存 NULL。
func (s *Store) CreateConversation(ctx context.Context, title, taskID string) (Conversation, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO conversation (title, task_id)
		VALUES (NULLIF($1,''), NULLIF($2,'')::uuid)
		RETURNING `+convCols, title, taskID)
	var c Conversation
	if err := scanConversation(row, &c); err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return c, nil
}

// GetConversation 按主键读会话（不限 status）。
func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+convCols+" FROM conversation WHERE id=$1", id)
	var c Conversation
	if err := scanConversation(row, &c); err != nil {
		return Conversation{}, fmt.Errorf("get conversation %s: %w", id, err)
	}
	return c, nil
}

// DeleteConversation 删除会话及其消息（message FK ON DELETE CASCADE 自动连带删）。
// 不动关联的 task / finding（task_id FK 是 SET NULL，渗透成果以 task 为根，不因删会话丢失）。
// id 不存在返回 not found 错误。
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

// ResolveTaskID 解析会话关联的 task id（用于聚合 llm_invocation / tool_invocation 用量）。
// 两轨统一：conversation.task_id 即所属 task；纯聊天无关联 → 返回空串（调用方据此返回零用量）。
func (s *Store) ResolveTaskID(ctx context.Context, convID string) (string, error) {
	c, err := s.GetConversation(ctx, convID)
	if err != nil {
		return "", err
	}
	return c.TaskID, nil
}

// ResolveConvByTask 反向解析：task id → 绑定的会话 id（思维链来源）。
// 一 task 常对应一会话；若有多条（follow-up 复活等）取最近更新的一条。
// 无绑定会话（纯 passive 自动路径 / task 不存在）返回空串——调用方据此只出成果链，不报错。
func (s *Store) ResolveConvByTask(ctx context.Context, taskID string) (string, error) {
	if taskID == "" {
		return "", nil
	}
	var convID string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM conversation WHERE task_id = $1 ORDER BY updated_at DESC LIMIT 1`,
		taskID).Scan(&convID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve conversation for task %s: %w", taskID, err)
	}
	return convID, nil
}

// IsRunActive 返回本会话当前是否有正在运行的扫描（权威：后端 task 终态，非客户端计时）。
// 关联 task.status='active'（completed/aborted 为终态）；纯聊天 / 已结束返回 false。
// 前端据此显示"agent 工作中"指示器，避免历史回灌误判。
func (s *Store) IsRunActive(ctx context.Context, convID string) (bool, error) {
	var running bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
		         SELECT 1 FROM task t JOIN conversation c ON c.task_id = t.id
		         WHERE c.id = $1 AND t.status = 'active'
		       )`, convID).Scan(&running)
	if err != nil {
		return false, fmt.Errorf("check run active for conversation %s: %w", convID, err)
	}
	return running, nil
}

// WallclockMs 返回本会话关联 task 的「纯工作」墙钟时长（毫秒）——发起→完成的流逝时间扣除停顿。
// 跑中用 now()-created_at，结束用 ended_at-created_at；再减 task.paused_ms（多轮 follow-up
// 复活间的用户停顿累计，见 task.Reopen）。首次扫描 paused_ms=0。纯聊天/无关联 task 返回 0。
func (s *Store) WallclockMs(ctx context.Context, convID string) (int64, error) {
	var ms *int64
	err := s.pool.QueryRow(ctx, `
		SELECT ((EXTRACT(EPOCH FROM (COALESCE(t.ended_at, now()) - t.created_at)) * 1000)::bigint - t.paused_ms)
		FROM task t JOIN conversation c ON c.task_id = t.id
		WHERE c.id = $1
		LIMIT 1`, convID).Scan(&ms)
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

// ListConversations 按 updated_at DESC 分页列出会话（UI 列表）。offset<0 视为 0。
// scenarioID 非空时按关联 task.scenario_id 过滤——分页边界必须建立在过滤后的集合上，
// 否则「前端按场景过滤 + 后端按 offset 翻页」两者独立计数会导致页码与实际条数错位。
//
// 翻页用 offset（非 keyset 游标）：会话排序键是 updated_at，活跃会话会被追加消息"顶到最前"、
// 破坏单调性——不像 llm_invocation 按自增 id 排序那样能用 keyset。这个数据量级（个人工具,
// 不是海量 feed）offset 分页足够，也更简单：翻页时排序小幅重排是可接受的权衡。
//
// hasMore 判定用「多取一条」（LIMIT limit+1），不额外发 COUNT 查询。
func (s *Store) ListConversations(ctx context.Context, limit, offset int, scenarioID string) ([]Conversation, bool, error) {
	limit = clampLimit(limit)
	if offset < 0 {
		offset = 0
	}
	// run_status：派生「真实运行态」——取关联 task.status（active/completed/aborted）。
	// 无关联 task（纯聊天）→ 空串。前端列表据此显示准确状态。
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, COALESCE(c.title,''), COALESCE(c.task_id::text,''),
			c.created_at, c.updated_at,
			COALESCE(t.status, '') AS run_status,
			COALESCE(t.scenario_id, '') AS scenario_id,
			COALESCE((SELECT count(*) FROM finding f WHERE f.task_id = c.task_id), 0) AS finding_count
		FROM conversation c
		LEFT JOIN task t ON t.id = c.task_id
		WHERE ($3 = '' OR t.scenario_id = $3)
		ORDER BY c.updated_at DESC LIMIT $1 OFFSET $2`, limit+1, offset, scenarioID)
	if err != nil {
		return nil, false, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := scanConversationWithRun(rows, &c); err != nil {
			return nil, false, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// SetTitle 回填会话标题（首条消息摘要）。空 title 存 NULL。
func (s *Store) SetTitle(ctx context.Context, id, title string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE conversation SET title=NULLIF($1,''), updated_at=now() WHERE id=$2", title, id)
	if err != nil {
		return fmt.Errorf("set conversation %s title: %w", id, err)
	}
	return nil
}

// LinkTask 把会话关联到一个 task（会话发起扫描后回填）。
func (s *Store) LinkTask(ctx context.Context, convID, taskID string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE conversation SET task_id=NULLIF($1,'')::uuid, updated_at=now() WHERE id=$2", taskID, convID)
	if err != nil {
		return fmt.Errorf("link conversation %s task: %w", convID, err)
	}
	return nil
}

const msgCols = "seq, id, conversation_id, role, kind, content, metadata, created_at"

// AppendMessage 往会话追加一条消息（普通消息或 agent 过程事件），并刷新 conversation.updated_at
// 让会话列表按活跃排序。metadata 可为 nil。
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
	// touch updated_at（仅真实会话消息 user/assistant）：失败仅影响列表排序，消息已落库——不报错。
	// KindEvent 是高频 agent 过程事件（active 一次几百条），不参与会话列表排序，跳过这次 DB 往返
	// ——省掉热路径上每事件的第二次同步写。
	if kind != KindEvent {
		_, _ = s.pool.Exec(ctx, "UPDATE conversation SET updated_at=now() WHERE id=$1", convID)
	}
	return m, nil
}

// GetMessage 按 id 取单条消息，用于「点开节点看原文」按需拉取——调用方（如执行图详情面板）
// 只需要 selected.ref 指向的这一条正文，不必像 ListMessages 那样翻页拉整段会话历史。
// convID 一并校验：防止跨会话用别处泄漏的 message id 越权读取到其他会话的内容。
// 未找到（id 不存在 / 不属于该会话）返回 pgx.ErrNoRows，调用方按需转 404。
func (s *Store) GetMessage(ctx context.Context, convID, msgID string) (Message, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+msgCols+" FROM message WHERE conversation_id=$1 AND id=$2", convID, msgID)
	var m Message
	if err := scanMessage(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, err
		}
		return Message{}, fmt.Errorf("get message %s of %s: %w", msgID, convID, err)
	}
	return m, nil
}

// ListMessages 取会话内 seq>afterSeq 的消息（按 seq 升序）。afterSeq=0 取全部（回看）；
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

// ListRecentDialog 取会话内最近 limit 条「普通会话消息」（KindMessage，即 user/assistant/system；
// 滤掉高频 agent 过程事件 KindEvent），按 seq 升序返回。供 agent 读会话历史当工作上下文
// （阶段0：active 多轮追问连贯性）。limit ≤ 0 用默认。
func (s *Store) ListRecentDialog(ctx context.Context, convID string, limit int) ([]Message, error) {
	limit = clampLimit(limit)
	// 先按 seq DESC 取最近 limit 条，再在 Go 里反转成升序（会话历史按时间正序喂 LLM）。
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
	return r.Scan(&c.ID, &c.Title, &c.TaskID, &c.CreatedAt, &c.UpdatedAt)
}

// scanConversationWithRun 多扫 run_status + scenario_id + finding_count（派生态 + 场景 + 漏洞数，见 ListConversations）。
func scanConversationWithRun(r scanRow, c *Conversation) error {
	return r.Scan(&c.ID, &c.Title, &c.TaskID, &c.CreatedAt, &c.UpdatedAt, &c.RunStatus, &c.ScenarioID, &c.FindingCount)
}

// RunStatus 返回会话关联 task 的「真实运行态」（task.status：active/completed/aborted；
// 纯聊天空串）——供顶部状态栏显示三态，区别于 IsRunActive 的二元布尔。
// 用 conversation.status 是僵尸字段（默认 active 从不更新），不可用于显示。
func (s *Store) RunStatus(ctx context.Context, convID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(t.status, '')
		FROM conversation c
		LEFT JOIN task t ON t.id = c.task_id
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
