// Package metrics 提供 Monitor 的时间序列指标计算
package metrics

import (
	"sync"
	"time"
)

// Bucket 是时间窗口中的一个桶
type Bucket struct {
	Timestamp time.Time // 桶的时间戳（向下取整到 bucketSize）
	Count     int       // 事件数量
	Sum       float64   // 值的总和（用于计算平均值）
}

// SlidingWindow 是滑动时间窗口
//
// 用于计算"每小时 action 数"、"result/action 比率"等时间窗口指标。
// 采用固定大小的时间桶（如 10 分钟），保留最近 N 个桶（如 6 个桶 = 1 小时）。
//
// 示例：
//   window := NewSlidingWindow(10*time.Minute, 1*time.Hour)
//   window.Add(1)  // 添加一个事件
//   rate := window.Rate()  // 计算每小时速率
type SlidingWindow struct {
	buckets    []Bucket
	bucketSize time.Duration // 每个桶的时间跨度（如 10 分钟）
	windowSize time.Duration // 窗口总大小（如 1 小时）
	maxBuckets int           // 最多保留的桶数
	mu         sync.RWMutex
}

// NewSlidingWindow 创建滑动窗口
//
// bucketSize: 每个桶的时间跨度（如 10*time.Minute）
// windowSize: 窗口总大小（如 1*time.Hour）
//
// 窗口会保留 windowSize/bucketSize 个桶。
func NewSlidingWindow(bucketSize, windowSize time.Duration) *SlidingWindow {
	maxBuckets := int(windowSize / bucketSize)
	if maxBuckets < 1 {
		maxBuckets = 1
	}

	return &SlidingWindow{
		buckets:    make([]Bucket, 0, maxBuckets),
		bucketSize: bucketSize,
		windowSize: windowSize,
		maxBuckets: maxBuckets,
	}
}

// Add 添加一个值到窗口
//
// value: 要添加的值（如果只计数，传 1；如果要计算平均值，传实际值）
func (w *SlidingWindow) Add(value float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	bucketTime := now.Truncate(w.bucketSize)

	// 清理过期桶（在添加前清理，保证窗口大小）
	w.cleanExpiredBuckets(now)

	// 找到或创建当前时间桶
	if len(w.buckets) == 0 || w.buckets[len(w.buckets)-1].Timestamp.Before(bucketTime) {
		// 创建新桶
		w.buckets = append(w.buckets, Bucket{
			Timestamp: bucketTime,
			Count:     1,
			Sum:       value,
		})
	} else if w.buckets[len(w.buckets)-1].Timestamp.Equal(bucketTime) {
		// 累加到最后一个桶
		last := &w.buckets[len(w.buckets)-1]
		last.Count++
		last.Sum += value
	}

	// 再次清理，确保不超过 maxBuckets
	if len(w.buckets) > w.maxBuckets {
		w.buckets = w.buckets[len(w.buckets)-w.maxBuckets:]
	}
}

// Rate 计算速率（事件数 / 小时）
//
// 返回窗口内的平均速率（每小时事件数）。
func (w *SlidingWindow) Rate() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.buckets) == 0 {
		return 0
	}

	// 计算总事件数
	var totalCount int
	for _, bucket := range w.buckets {
		totalCount += bucket.Count
	}

	// 计算速率：总数 / 时间跨度（小时）
	hours := w.windowSize.Hours()
	if hours == 0 {
		return 0
	}

	return float64(totalCount) / hours
}

// Average 计算平均值
//
// 返回窗口内所有值的平均值。
func (w *SlidingWindow) Average() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.buckets) == 0 {
		return 0
	}

	var totalSum float64
	var totalCount int
	for _, bucket := range w.buckets {
		totalSum += bucket.Sum
		totalCount += bucket.Count
	}

	if totalCount == 0 {
		return 0
	}

	return totalSum / float64(totalCount)
}

// Count 返回窗口内的总事件数
func (w *SlidingWindow) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var totalCount int
	for _, bucket := range w.buckets {
		totalCount += bucket.Count
	}

	return totalCount
}

// cleanExpiredBuckets 清理过期桶（调用方必须持有写锁）
func (w *SlidingWindow) cleanExpiredBuckets(now time.Time) {
	cutoff := now.Add(-w.windowSize)

	// 找到第一个未过期的桶
	firstValid := 0
	for i, bucket := range w.buckets {
		if bucket.Timestamp.After(cutoff) || bucket.Timestamp.Equal(cutoff) {
			firstValid = i
			break
		}
		firstValid = i + 1
	}

	// 移除过期桶
	if firstValid > 0 && firstValid <= len(w.buckets) {
		w.buckets = w.buckets[firstValid:]
	}
}
