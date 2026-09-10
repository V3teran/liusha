package knowledgegraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
)

// RoadmapStepStatus 是 RoadmapStep 的状态
//
// 设计理念：
// - RoadmapStep 的状态是简化的高层状态
// - 与 Action 的复杂状态（State）分层
// - 状态转换由 Planner 和 Executor 协同管理
type RoadmapStepStatus string

const (
	StepTodo     RoadmapStepStatus = "todo"     // 待执行（Planner 已规划但未派发 Action）
	StepActive   RoadmapStepStatus = "active"   // 执行中（已派发 Action，正在执行）
	StepComplete RoadmapStepStatus = "complete" // 已完成（所有派生的 Action 都完成）
	StepSkipped  RoadmapStepStatus = "skipped"  // 已跳过（Planner 决定跳过此步骤）
)

// RoadmapStep 是 Planner 规划的高层步骤
//
// 设计理念：
// 1. Roadmap 是探索式任务的动态规划
// 2. 每个 Step 是一个可验证的里程碑（中粒度）
// 3. Planner 根据执行结果动态调整 Roadmap（完全替换）
// 4. 一个 Step 可以派发多个 Action（1:N 映射）
//
// 与 Action 的关系：
// - RoadmapStep：高层目标（"测试 SQL 注入"）
// - Action：低层执行（"测试 UNION 注入"、"测试布尔盲注"）
//
// 状态转换：
// - todo → active：Planner 派发第一个 Action
// - active → complete：所有派生的 Action 完成
// - todo/active → skipped：Planner 决定跳过
type RoadmapStep struct {
	TaskID    string                 `json:"task_id"`
	Step      float64                `json:"step"`       // 步骤编号（支持小数，如 1.5）
	Objective string                 `json:"objective"`  // 步骤目标（自然语言）
	Status    RoadmapStepStatus      `json:"status"`     // 状态
	DependsOn []float64              `json:"depends_on"` // 依赖的步骤编号
	Context   map[string]interface{} `json:"context"`    // 上下文数据
	Rationale string                 `json:"rationale"`  // Planner 的推理过程
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// SaveRoadmap 保存整个 Roadmap（完全替换式更新）
//
// 设计理念：
// - Planner 每次唤醒时重新生成完整的 Roadmap
// - 采用完全替换而非增量更新，简化 LLM 认知负担
// - 事务保证原子性：要么全部成功，要么回滚
//
// 参数：
// - steps：完整的 Roadmap（按 step 排序）
//
// 注意：
// - 此方法会删除旧的 Roadmap，请确保 steps 包含所有需要保留的步骤
func (s *Store) SaveRoadmap(ctx context.Context, taskID string, steps []RoadmapStep) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 删除旧的 Roadmap
	_, err = tx.Exec(ctx, `DELETE FROM wm_roadmap_step WHERE task_id = $1`, taskID)
	if err != nil {
		return fmt.Errorf("delete old roadmap: %w", err)
	}

	// 插入新的 Roadmap
	for _, step := range steps {
		contextJSON, _ := json.Marshal(step.Context)

		_, err = tx.Exec(ctx, `
			INSERT INTO wm_roadmap_step (
				task_id, step, objective, status, depends_on, context, rationale, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, taskID, step.Step, step.Objective, step.Status, pq.Array(step.DependsOn),
			contextJSON, step.Rationale, time.Now(), time.Now())

		if err != nil {
			return fmt.Errorf("insert roadmap step %.1f: %w", step.Step, err)
		}
	}

	return tx.Commit(ctx)
}

// LoadRoadmap 加载整个 Roadmap（按 step 排序）
//
// 返回：
// - []RoadmapStep：按步骤编号升序排列
// - error：查询错误
func (s *Store) LoadRoadmap(ctx context.Context, taskID string) ([]RoadmapStep, error) {
	query := `
		SELECT step, objective, status, depends_on, context, rationale, created_at, updated_at
		FROM wm_roadmap_step
		WHERE task_id = $1
		ORDER BY step ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query roadmap: %w", err)
	}
	defer rows.Close()

	var steps []RoadmapStep
	for rows.Next() {
		var step RoadmapStep
		var contextJSON []byte
		var dependsOn []float64
		var rationale sql.NullString

		err := rows.Scan(
			&step.Step,
			&step.Objective,
			&step.Status,
			pq.Array(&dependsOn),
			&contextJSON,
			&rationale,
			&step.CreatedAt,
			&step.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan roadmap step: %w", err)
		}

		step.TaskID = taskID
		step.DependsOn = dependsOn
		if rationale.Valid {
			step.Rationale = rationale.String
		}

		// 解析 context
		if len(contextJSON) > 0 && string(contextJSON) != "null" {
			_ = json.Unmarshal(contextJSON, &step.Context)
		}

		steps = append(steps, step)
	}

	return steps, rows.Err()
}

// UpdateStepStatus 更新单个 Step 的状态
//
// 用途：
// - Planner 派发 Action 后：todo → active
// - 所有 Action 完成后：active → complete
// - Planner 决定跳过：任意状态 → skipped
func (s *Store) UpdateStepStatus(ctx context.Context, taskID string, step float64, status RoadmapStepStatus) error {
	query := `
		UPDATE wm_roadmap_step
		SET status = $3, updated_at = $4
		WHERE task_id = $1 AND step = $2
	`

	result, err := s.pool.Exec(ctx, query, taskID, step, status, time.Now())
	if err != nil {
		return fmt.Errorf("update step status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("step %.1f not found in task %s", step, taskID)
	}

	return nil
}

// UpdateStepContext 更新单个 Step 的上下文
//
// 用途：
// - Action 完成后，将发现的关键信息写入 Step.Context
// - Planner 下次唤醒时读取，用于调整后续 Roadmap
func (s *Store) UpdateStepContext(ctx context.Context, taskID string, step float64, context map[string]interface{}) error {
	contextJSON, _ := json.Marshal(context)

	query := `
		UPDATE wm_roadmap_step
		SET context = $3, updated_at = $4
		WHERE task_id = $1 AND step = $2
	`

	result, err := s.pool.Exec(ctx, query, taskID, step, contextJSON, time.Now())
	if err != nil {
		return fmt.Errorf("update step context: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("step %.1f not found in task %s", step, taskID)
	}

	return nil
}

// GetNextExecutableStep 获取下一个可执行的 Step
//
// 规则：
// - status = todo
// - 所有 depends_on 的步骤都是 complete
//
// 返回：
// - *RoadmapStep：下一个可执行的步骤
// - nil：没有可执行的步骤（全部完成或阻塞）
func (s *Store) GetNextExecutableStep(ctx context.Context, taskID string) (*RoadmapStep, error) {
	// 1. 加载整个 Roadmap
	steps, err := s.LoadRoadmap(ctx, taskID)
	if err != nil {
		return nil, err
	}

	// 2. 构建 completed 步骤集合
	completed := make(map[float64]bool)
	for _, st := range steps {
		if st.Status == StepComplete {
			completed[st.Step] = true
		}
	}

	// 3. 找到第一个可执行的 step
	for _, st := range steps {
		if st.Status != StepTodo {
			continue
		}

		// 检查依赖
		canExecute := true
		for _, dep := range st.DependsOn {
			if !completed[dep] {
				canExecute = false
				break
			}
		}

		if canExecute {
			return &st, nil
		}
	}

	return nil, nil // 没有可执行的
}

// GetStepByNumber 根据步骤编号获取 Step
func (s *Store) GetStepByNumber(ctx context.Context, taskID string, step float64) (*RoadmapStep, error) {
	query := `
		SELECT step, objective, status, depends_on, context, rationale, created_at, updated_at
		FROM wm_roadmap_step
		WHERE task_id = $1 AND step = $2
	`

	var st RoadmapStep
	var contextJSON []byte
	var dependsOn []float64
	var rationale sql.NullString

	err := s.pool.QueryRow(ctx, query, taskID, step).Scan(
		&st.Step,
		&st.Objective,
		&st.Status,
		pq.Array(&dependsOn),
		&contextJSON,
		&rationale,
		&st.CreatedAt,
		&st.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get step: %w", err)
	}

	st.TaskID = taskID
	st.DependsOn = dependsOn
	if rationale.Valid {
		st.Rationale = rationale.String
	}

	if len(contextJSON) > 0 && string(contextJSON) != "null" {
		_ = json.Unmarshal(contextJSON, &st.Context)
	}

	return &st, nil
}

// HasRoadmap 检查任务是否有 Roadmap
func (s *Store) HasRoadmap(ctx context.Context, taskID string) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM wm_roadmap_step WHERE task_id = $1`, taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count roadmap steps: %w", err)
	}
	return count > 0, nil
}

// GetActiveSteps 获取所有 active 状态的 Steps
//
// 用途：
// - 判断哪些 Step 正在执行
// - 统计进度
func (s *Store) GetActiveSteps(ctx context.Context, taskID string) ([]RoadmapStep, error) {
	query := `
		SELECT step, objective, status, depends_on, context, rationale, created_at, updated_at
		FROM wm_roadmap_step
		WHERE task_id = $1 AND status = $2
		ORDER BY step ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, StepActive)
	if err != nil {
		return nil, fmt.Errorf("query active steps: %w", err)
	}
	defer rows.Close()

	var steps []RoadmapStep
	for rows.Next() {
		var step RoadmapStep
		var contextJSON []byte
		var dependsOn []float64
		var rationale sql.NullString

		err := rows.Scan(
			&step.Step,
			&step.Objective,
			&step.Status,
			pq.Array(&dependsOn),
			&contextJSON,
			&rationale,
			&step.CreatedAt,
			&step.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}

		step.TaskID = taskID
		step.DependsOn = dependsOn
		if rationale.Valid {
			step.Rationale = rationale.String
		}

		if len(contextJSON) > 0 && string(contextJSON) != "null" {
			_ = json.Unmarshal(contextJSON, &step.Context)
		}

		steps = append(steps, step)
	}

	return steps, rows.Err()
}

// CountStepsByStatus 统计各状态的 Step 数量
//
// 用途：
// - 任务进度展示
// - 判断任务是否完成
//
// 返回：
// - map[RoadmapStepStatus]int：各状态的数量
func (s *Store) CountStepsByStatus(ctx context.Context, taskID string) (map[RoadmapStepStatus]int, error) {
	query := `
		SELECT status, COUNT(*)
		FROM wm_roadmap_step
		WHERE task_id = $1
		GROUP BY status
	`

	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("count steps by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[RoadmapStepStatus]int)
	for rows.Next() {
		var status RoadmapStepStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}

	return counts, rows.Err()
}

// FindNextStepNumber 找到下一个可用的步骤编号
//
// 用途：
// - Planner 插入新步骤时，确定步骤编号
// - 支持小数步骤（如在 1.0 和 2.0 之间插入 1.5）
//
// 算法：
// - 如果 Roadmap 为空，返回 1.0
// - 否则返回 max(step) + 1.0
func (s *Store) FindNextStepNumber(ctx context.Context, taskID string) (float64, error) {
	var maxStep sql.NullFloat64
	err := s.pool.QueryRow(ctx, `SELECT MAX(step) FROM wm_roadmap_step WHERE task_id = $1`, taskID).Scan(&maxStep)
	if err != nil {
		return 0, fmt.Errorf("find max step: %w", err)
	}

	if !maxStep.Valid {
		return 1.0, nil // 第一个步骤
	}

	return maxStep.Float64 + 1.0, nil
}

// InsertStepBetween 在两个步骤之间插入新步骤
//
// 用途：
// - Planner 动态插入步骤（如在 1.0 和 2.0 之间插入 1.5）
//
// 算法：
// - 新步骤编号 = (before + after) / 2
func (s *Store) InsertStepBetween(ctx context.Context, taskID string, before, after float64, objective string) (float64, error) {
	newStep := (before + after) / 2.0

	contextJSON, _ := json.Marshal(map[string]interface{}{})

	_, err := s.pool.Exec(ctx, `
		INSERT INTO wm_roadmap_step (
			task_id, step, objective, status, depends_on, context, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, taskID, newStep, objective, StepTodo, pq.Array([]float64{}), contextJSON, time.Now(), time.Now())

	if err != nil {
		return 0, fmt.Errorf("insert step between %.1f and %.1f: %w", before, after, err)
	}

	return newStep, nil
}

// RoadmapSummary 返回 Roadmap 的摘要信息
//
// 用途：
// - 提供给 Planner 的 System Prompt
// - 展示任务进度
type RoadmapSummary struct {
	TotalSteps     int
	TodoSteps      int
	ActiveSteps    int
	CompleteSteps  int
	SkippedSteps   int
	CompletionRate float64 // 完成率（0-1）
}

// GetRoadmapSummary 获取 Roadmap 摘要
func (s *Store) GetRoadmapSummary(ctx context.Context, taskID string) (*RoadmapSummary, error) {
	counts, err := s.CountStepsByStatus(ctx, taskID)
	if err != nil {
		return nil, err
	}

	total := 0
	for _, count := range counts {
		total += count
	}

	completionRate := 0.0
	if total > 0 {
		completionRate = float64(counts[StepComplete]) / float64(total)
	}

	return &RoadmapSummary{
		TotalSteps:     total,
		TodoSteps:      counts[StepTodo],
		ActiveSteps:    counts[StepActive],
		CompleteSteps:  counts[StepComplete],
		SkippedSteps:   counts[StepSkipped],
		CompletionRate: completionRate,
	}, nil
}

// SortStepsByNumber 按步骤编号排序（辅助函数）
func SortStepsByNumber(steps []RoadmapStep) {
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].Step < steps[j].Step
	})
}
