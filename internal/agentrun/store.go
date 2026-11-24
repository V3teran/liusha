package agentrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoRun 表示查询条件下不存在 agent run 行（LatestByTask 无行时返回）。
var ErrNoRun = errors.New("agent run 不存在")

// ErrLiveRunExists 表示该 task 已有存活 run（pending/running）——部分唯一索引
// agent_run_one_live_per_task 拒绝了第二个存活行的创建。agent run 有四个并发
// 生产者（下发展开/会话续接/暂停后继续/恢复监控重投），此哨兵是竞争的判定
// 信号：先建者赢，后来者按各自语义处理（幂等成功 / 跳过重投 / 复用既有 run）。
var ErrLiveRunExists = errors.New("该任务已有存活 run（pending/running），拒绝再建")

// Store 封装 agent_run 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scanRun() 的字段顺序一一对应。
const colsSelect = `id, ` +
	`task_id::text AS task_id, ` +
	`status, created_at, updated_at, heartbeat_at`

// Create 插入一行 pending agent 运行，返回新 id。run 不携带输入：它要做的事（任务目标）
// 在任务行与探索图上，开跑时读取。TaskID 必填（NOT NULL 外键）。该 task 已有存活 run 时
// 返回 ErrLiveRunExists。
func (s *Store) Create(ctx context.Context, p NewParams) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_run (task_id)
		VALUES ($1::uuid)
		RETURNING id`,
		p.TaskID,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrLiveRunExists
		}
		return "", fmt.Errorf("insert agent run: %w", err)
	}
	return id, nil
}

// SetRunning 把 pending agent 运行推进到 running；非 pending 视为非法转换。存活心跳从这里起算：
// 排队可能排了很久，建行时刻不能代表这轮 run 的存活。
func (s *Store) SetRunning(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='running', updated_at=now(), heartbeat_at=now()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return fmt.Errorf("set running %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent run %s not in pending state", id)
	}
	return nil
}

// Heartbeat 续约一轮 running 的存活心跳（恢复监控据此判定它是否还在被推进）。只认 running：
// 续约与 run 收尾并发时，不把已经结束的 run 写成刚活过。
func (s *Store) Heartbeat(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET heartbeat_at=now()
		WHERE id=$1 AND status='running'`, id)
	if err != nil {
		return fmt.Errorf("heartbeat agent run %s: %w", id, err)
	}
	return nil
}

// SetCompleted 把 pending|running 推进到 completed。
func (s *Store) SetCompleted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='completed', updated_at=now()
		WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("set completed %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent run %s not in pending|running state", id)
	}
	return nil
}

// SetError 把 pending|running 推进到 error（恢复监控按 error run 计重投次数）。出错细节由
// 调用方记日志；任务因此终止时原因写在任务行。
func (s *Store) SetError(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='error', updated_at=now()
		WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("set error %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent run %s not in pending|running state", id)
	}
	return nil
}

// SetAborted 把 pending|running 推进到 aborted（用于 task abort 级联）。
func (s *Store) SetAborted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='aborted', updated_at=now()
		WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("set aborted %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent run %s not in pending|running state", id)
	}
	return nil
}

// AbortPending 把仍为 pending 的运行置为 aborted（CAS：只认 pending），返回是否发生迁移。
// 用于回收队列里已不存在的孤儿 run：与 handler 领取（SetRunning，同样只认 pending）互斥，
// 已被领取的运行不受影响。
func (s *Store) AbortPending(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='aborted', updated_at=now()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, fmt.Errorf("abort pending %s: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetByID 按主键读取 agent 运行行。
func (s *Store) GetByID(ctx context.Context, id string) (Run, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+colsSelect+` FROM agent_run WHERE id=$1`, id)
	var r Run
	if err := scanRun(row, &r); err != nil {
		return Run{}, fmt.Errorf("get agent run %s: %w", id, err)
	}
	return r, nil
}

// LatestByTask 取任务最近一条 run（created_at 倒序第一条）。无 run 时返回
// ErrNoRun——调用方以此区分「从未跑过」与「查询失败」。
func (s *Store) LatestByTask(ctx context.Context, taskID string) (*Run, error) {
	var r Run
	if err := scanRun(s.pool.QueryRow(ctx,
		`SELECT `+colsSelect+` FROM agent_run WHERE task_id=$1::uuid ORDER BY created_at DESC LIMIT 1`,
		taskID), &r); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoRun
		}
		return nil, fmt.Errorf("latest agent run by task: %w", err)
	}
	return &r, nil
}

// LiveByTask 取任务当前存活的一轮 run（pending / running；每任务至多一个）；没有时返回 nil。
func (s *Store) LiveByTask(ctx context.Context, taskID string) (*Run, error) {
	var r Run
	if err := scanRun(s.pool.QueryRow(ctx,
		`SELECT `+colsSelect+` FROM agent_run WHERE task_id=$1::uuid AND status IN ('pending','running')`,
		taskID), &r); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("live agent run by task: %w", err)
	}
	return &r, nil
}

// ListByTask 按 created_at 升序列出 task 下的 agent 运行，最多 limit 条。
func (s *Store) ListByTask(ctx context.Context, taskID string, limit int) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+colsSelect+` FROM agent_run WHERE task_id=$1::uuid ORDER BY created_at ASC LIMIT $2`,
		taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent runs by task: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		if err := scanRun(rows, &r); err != nil {
			return nil, fmt.Errorf("scan agent run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent runs: %w", err)
	}
	return out, nil
}

// scanRun 是 colsSelect 列序的统一反序列化点。
func scanRun(r db.RowScanner, t *Run) error {
	return r.Scan(&t.ID, &t.TaskID, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.HeartbeatAt)
}
