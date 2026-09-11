package memory

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/V3teran/liusha/internal/framework/core"
)

// Checkpointer 是内存版检查点管理器（用于开发和测试）。
type Checkpointer struct {
	mu          sync.RWMutex
	checkpoints map[core.CheckpointID]*core.Checkpoint
	byTask      map[string][]core.CheckpointID // taskID -> 检查点 ID 列表（按时间排序）
}

// NewCheckpointer 创建内存版检查点管理器。
func NewCheckpointer() *Checkpointer {
	return &Checkpointer{
		checkpoints: make(map[core.CheckpointID]*core.Checkpoint),
		byTask:      make(map[string][]core.CheckpointID),
	}
}

// Save 保存检查点。
func (c *Checkpointer) Save(ctx context.Context, checkpoint core.Checkpoint) (core.CheckpointID, error) {
	// 生成 ID
	id := core.CheckpointID(uuid.New().String())
	checkpoint.ID = id
	checkpoint.CreatedAt = time.Now()

	// 计算大小
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return "", err
	}
	checkpoint.SizeBytes = int64(len(data))

	c.mu.Lock()
	defer c.mu.Unlock()

	// 保存检查点
	c.checkpoints[id] = &checkpoint

	// 添加到任务索引
	c.byTask[checkpoint.TaskID] = append(c.byTask[checkpoint.TaskID], id)

	return id, nil
}

// Load 加载检查点。
func (c *Checkpointer) Load(ctx context.Context, id core.CheckpointID) (*core.Checkpoint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	checkpoint, exists := c.checkpoints[id]
	if !exists {
		return nil, core.ErrCheckpointNotFound{CheckpointID: id}
	}

	// 返回副本，避免外部修改
	copied := *checkpoint
	return &copied, nil
}

// List 列出任务的所有检查点（按时间倒序）。
func (c *Checkpointer) List(ctx context.Context, taskID string, limit int) ([]core.CheckpointMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := c.byTask[taskID]
	if len(ids) == 0 {
		return []core.CheckpointMeta{}, nil
	}

	// 按时间倒序排序
	sorted := make([]*core.Checkpoint, 0, len(ids))
	for _, id := range ids {
		if cp, ok := c.checkpoints[id]; ok {
			sorted = append(sorted, cp)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})

	// 限制数量
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}

	// 转换为元信息
	result := make([]core.CheckpointMeta, len(sorted))
	for i, cp := range sorted {
		result[i] = core.CheckpointMeta{
			ID:        cp.ID,
			TaskID:    cp.TaskID,
			Phase:     cp.Phase,
			Labels:    cp.Labels,
			CreatedAt: cp.CreatedAt,
			SizeBytes: cp.SizeBytes,
		}
	}

	return result, nil
}

// Delete 删除检查点。
func (c *Checkpointer) Delete(ctx context.Context, id core.CheckpointID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	checkpoint, exists := c.checkpoints[id]
	if !exists {
		return core.ErrCheckpointNotFound{CheckpointID: id}
	}

	// 从索引中移除
	ids := c.byTask[checkpoint.TaskID]
	for i, cid := range ids {
		if cid == id {
			c.byTask[checkpoint.TaskID] = append(ids[:i], ids[i+1:]...)
			break
		}
	}

	// 删除检查点
	delete(c.checkpoints, id)

	return nil
}

// Latest 获取任务的最新检查点。
func (c *Checkpointer) Latest(ctx context.Context, taskID string) (*core.Checkpoint, error) {
	metas, err := c.List(ctx, taskID, 1)
	if err != nil {
		return nil, err
	}

	if len(metas) == 0 {
		return nil, core.ErrCheckpointNotFound{CheckpointID: core.CheckpointID(taskID + "-latest")}
	}

	return c.Load(ctx, metas[0].ID)
}

// Prune 清理过期检查点（保留最近 N 个）。
func (c *Checkpointer) Prune(ctx context.Context, taskID string, keepCount int) error {
	metas, err := c.List(ctx, taskID, 0) // 获取所有
	if err != nil {
		return err
	}

	if len(metas) <= keepCount {
		return nil // 无需清理
	}

	// 删除多余的检查点
	toDelete := metas[keepCount:]
	for _, meta := range toDelete {
		if err := c.Delete(ctx, meta.ID); err != nil {
			return err
		}
	}

	return nil
}

// Clear 清空所有检查点（仅测试用）。
func (c *Checkpointer) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkpoints = make(map[core.CheckpointID]*core.Checkpoint)
	c.byTask = make(map[string][]core.CheckpointID)
}
