package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// StreamRecorder 是流式记录器，支持录制和回放。
type StreamRecorder struct {
	mu       sync.RWMutex
	records  map[string][]StreamEvent // taskID -> events
	filePath string
}

// NewStreamRecorder 创建流式记录器。
func NewStreamRecorder(filePath string) *StreamRecorder {
	return &StreamRecorder{
		records:  make(map[string][]StreamEvent),
		filePath: filePath,
	}
}

// Record 录制流。
func (r *StreamRecorder) Record(ctx context.Context, taskID string, input <-chan StreamEvent) error {
	r.mu.Lock()
	r.records[taskID] = make([]StreamEvent, 0)
	r.mu.Unlock()

	for {
		select {
		case event, ok := <-input:
			if !ok {
				return nil // 流结束
			}

			r.mu.Lock()
			r.records[taskID] = append(r.records[taskID], event)
			r.mu.Unlock()

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Replay 回放流。
func (r *StreamRecorder) Replay(ctx context.Context, taskID string) (<-chan StreamEvent, error) {
	r.mu.RLock()
	events, exists := r.records[taskID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no recording found for task: %s", taskID)
	}

	output := make(chan StreamEvent, 10)

	go func() {
		defer close(output)

		for _, event := range events {
			select {
			case output <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, nil
}

// ReplayWithTiming 按原始时间间隔回放流。
func (r *StreamRecorder) ReplayWithTiming(ctx context.Context, taskID string) (<-chan StreamEvent, error) {
	r.mu.RLock()
	events, exists := r.records[taskID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no recording found for task: %s", taskID)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("empty recording for task: %s", taskID)
	}

	output := make(chan StreamEvent, 10)

	go func() {
		defer close(output)

		for i, event := range events {
			// 计算延迟
			var delay time.Duration
			if i > 0 {
				prevTime := events[i-1].Data.(map[string]any)["timestamp"].(time.Time)
				currTime := event.Data.(map[string]any)["timestamp"].(time.Time)
				delay = currTime.Sub(prevTime)
			}

			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}

			select {
			case output <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, nil
}

// Save 保存录制到文件。
func (r *StreamRecorder) Save(ctx context.Context, taskID string) error {
	r.mu.RLock()
	events, exists := r.records[taskID]
	r.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no recording found for task: %s", taskID)
	}

	// 序列化
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}

	// 写入文件
	filePath := fmt.Sprintf("%s/%s.json", r.filePath, taskID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

// Load 从文件加载录制。
func (r *StreamRecorder) Load(ctx context.Context, taskID string) error {
	filePath := fmt.Sprintf("%s/%s.json", r.filePath, taskID)

	// 读取文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// 反序列化
	var events []StreamEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return fmt.Errorf("unmarshal events: %w", err)
	}

	r.mu.Lock()
	r.records[taskID] = events
	r.mu.Unlock()

	return nil
}

// Delete 删除录制。
func (r *StreamRecorder) Delete(taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.records[taskID]; !exists {
		return fmt.Errorf("no recording found for task: %s", taskID)
	}

	delete(r.records, taskID)

	// 删除文件
	filePath := fmt.Sprintf("%s/%s.json", r.filePath, taskID)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file: %w", err)
	}

	return nil
}

// List 列出所有录制。
func (r *StreamRecorder) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	taskIDs := make([]string, 0, len(r.records))
	for taskID := range r.records {
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

// GetStats 获取录制统计信息。
func (r *StreamRecorder) GetStats(taskID string) (RecordingStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events, exists := r.records[taskID]
	if !exists {
		return RecordingStats{}, fmt.Errorf("no recording found for task: %s", taskID)
	}

	stats := RecordingStats{
		TaskID:      taskID,
		EventCount:  len(events),
		EventTypes:  make(map[string]int),
	}

	if len(events) > 0 {
		stats.FirstEvent = events[0]
		stats.LastEvent = events[len(events)-1]

		// 统计事件类型
		for _, event := range events {
			stats.EventTypes[event.Type]++
		}
	}

	return stats, nil
}

// RecordingStats 是录制的统计信息。
type RecordingStats struct {
	TaskID     string         `json:"task_id"`
	EventCount int            `json:"event_count"`
	EventTypes map[string]int `json:"event_types"`
	FirstEvent StreamEvent    `json:"first_event"`
	LastEvent  StreamEvent    `json:"last_event"`
}

// ============================================
// 实时录制器（边录边存）
// ============================================

// LiveRecorder 实时录制器（边读边写文件）。
type LiveRecorder struct {
	file   *os.File
	mu     sync.Mutex
	closed bool
}

// NewLiveRecorder 创建实时录制器。
func NewLiveRecorder(filePath string) (*LiveRecorder, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	// 写入 JSON 数组开头
	if _, err := file.WriteString("[\n"); err != nil {
		file.Close()
		return nil, err
	}

	return &LiveRecorder{
		file: file,
	}, nil
}

// Record 录制事件。
func (l *LiveRecorder) Record(event StreamEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return fmt.Errorf("recorder closed")
	}

	// 序列化事件
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// 写入文件（JSON 数组元素）
	if _, err := l.file.Write(data); err != nil {
		return err
	}

	if _, err := l.file.WriteString(",\n"); err != nil {
		return err
	}

	return nil
}

// Close 关闭录制器。
func (l *LiveRecorder) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.closed = true

	// 写入 JSON 数组结尾
	if _, err := l.file.WriteString("]\n"); err != nil {
		return err
	}

	return l.file.Close()
}
