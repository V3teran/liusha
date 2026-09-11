package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/V3teran/liusha/internal/framework/middleware"
)

// HumanInputStore 是 PostgreSQL 版人工输入存储。
type HumanInputStore struct {
	pool *pgxpool.Pool
}

// NewHumanInputStore 创建 PostgreSQL 版存储。
func NewHumanInputStore(pool *pgxpool.Pool) *HumanInputStore {
	return &HumanInputStore{pool: pool}
}

// SaveRequest 保存请求。
func (s *HumanInputStore) SaveRequest(ctx context.Context, req middleware.HumanInputRequest) error {
	choicesJSON, _ := json.Marshal(req.Choices)
	metadataJSON, _ := json.Marshal(req.Metadata)

	query := `
		INSERT INTO framework_human_input_request (
			id, task_id, node_id, prompt, input_type, choices, default_value,
			timeout_sec, status, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := s.pool.Exec(ctx, query,
		req.ID,
		req.TaskID,
		req.NodeID,
		req.Prompt,
		req.InputType,
		choicesJSON,
		req.DefaultValue,
		req.TimeoutSec,
		"pending",
		metadataJSON,
		req.CreatedAt,
	)

	return err
}

// SaveResponse 保存响应。
func (s *HumanInputStore) SaveResponse(ctx context.Context, resp middleware.HumanInputResponse) error {
	// 开始事务
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 保存响应
	query := `
		INSERT INTO framework_human_input_response (
			request_id, task_id, node_id, value, approved, submitter, submitted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (request_id) DO UPDATE SET
			value = EXCLUDED.value,
			approved = EXCLUDED.approved,
			submitter = EXCLUDED.submitter,
			submitted_at = EXCLUDED.submitted_at
	`

	_, err = tx.Exec(ctx, query,
		resp.RequestID,
		resp.TaskID,
		resp.NodeID,
		resp.Value,
		resp.Approved,
		resp.Submitter,
		resp.SubmittedAt,
	)

	if err != nil {
		return err
	}

	// 更新请求状态
	updateQuery := `
		UPDATE framework_human_input_request
		SET status = 'completed'
		WHERE id = $1
	`

	_, err = tx.Exec(ctx, updateQuery, resp.RequestID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetRequest 获取请求。
func (s *HumanInputStore) GetRequest(ctx context.Context, requestID string) (*middleware.HumanInputRequest, error) {
	query := `
		SELECT id, task_id, node_id, prompt, input_type, choices, default_value,
		       timeout_sec, status, metadata, created_at
		FROM framework_human_input_request
		WHERE id = $1
	`

	var req middleware.HumanInputRequest
	var choicesJSON, metadataJSON []byte

	err := s.pool.QueryRow(ctx, query, requestID).Scan(
		&req.ID,
		&req.TaskID,
		&req.NodeID,
		&req.Prompt,
		&req.InputType,
		&choicesJSON,
		&req.DefaultValue,
		&req.TimeoutSec,
		// 跳过 status（存在 metadata 中）
		&metadataJSON,
		&req.CreatedAt,
	)

	if err != nil {
		return nil, err
	}

	// 反序列化
	if len(choicesJSON) > 0 {
		json.Unmarshal(choicesJSON, &req.Choices)
	}
	if len(metadataJSON) > 0 {
		json.Unmarshal(metadataJSON, &req.Metadata)
	}

	return &req, nil
}

// GetResponse 获取响应。
func (s *HumanInputStore) GetResponse(ctx context.Context, requestID string) (*middleware.HumanInputResponse, error) {
	query := `
		SELECT request_id, task_id, node_id, value, approved, submitter, submitted_at
		FROM framework_human_input_response
		WHERE request_id = $1
	`

	var resp middleware.HumanInputResponse

	err := s.pool.QueryRow(ctx, query, requestID).Scan(
		&resp.RequestID,
		&resp.TaskID,
		&resp.NodeID,
		&resp.Value,
		&resp.Approved,
		&resp.Submitter,
		&resp.SubmittedAt,
	)

	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// ListRequests 列出请求。
func (s *HumanInputStore) ListRequests(ctx context.Context, filter middleware.HumanInputFilter) ([]middleware.HumanInputRequest, error) {
	query := `
		SELECT id, task_id, node_id, prompt, input_type, choices, default_value,
		       timeout_sec, status, metadata, created_at
		FROM framework_human_input_request
		WHERE 1=1
	`

	args := []interface{}{}
	argIdx := 1

	// 构建查询条件
	if filter.TaskID != "" {
		query += ` AND task_id = $` + string(rune('0'+argIdx))
		args = append(args, filter.TaskID)
		argIdx++
	}

	if filter.Status != "" {
		query += ` AND status = $` + string(rune('0'+argIdx))
		args = append(args, filter.Status)
		argIdx++
	}

	if !filter.StartTime.IsZero() {
		query += ` AND created_at >= $` + string(rune('0'+argIdx))
		args = append(args, filter.StartTime)
		argIdx++
	}

	if !filter.EndTime.IsZero() {
		query += ` AND created_at <= $` + string(rune('0'+argIdx))
		args = append(args, filter.EndTime)
		argIdx++
	}

	// 排序
	query += ` ORDER BY created_at DESC`

	// 分页
	if filter.Limit > 0 {
		query += ` LIMIT $` + string(rune('0'+argIdx))
		args = append(args, filter.Limit)
		argIdx++
	}

	if filter.Offset > 0 {
		query += ` OFFSET $` + string(rune('0'+argIdx))
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []middleware.HumanInputRequest

	for rows.Next() {
		var req middleware.HumanInputRequest
		var choicesJSON, metadataJSON []byte
		var status string

		err := rows.Scan(
			&req.ID,
			&req.TaskID,
			&req.NodeID,
			&req.Prompt,
			&req.InputType,
			&choicesJSON,
			&req.DefaultValue,
			&req.TimeoutSec,
			&status,
			&metadataJSON,
			&req.CreatedAt,
		)

		if err != nil {
			return nil, err
		}

		// 反序列化
		if len(choicesJSON) > 0 {
			json.Unmarshal(choicesJSON, &req.Choices)
		}
		if len(metadataJSON) > 0 {
			json.Unmarshal(metadataJSON, &req.Metadata)
		}

		result = append(result, req)
	}

	return result, nil
}
