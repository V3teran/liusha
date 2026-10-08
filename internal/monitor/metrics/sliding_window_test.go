package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSlidingWindow_Add(t *testing.T) {
	window := NewSlidingWindow(1*time.Minute, 5*time.Minute)

	// 添加几个值
	window.Add(1)
	window.Add(1)
	window.Add(1)

	// 验证计数
	count := window.Count()
	assert.Equal(t, 3, count, "应该有 3 个事件")
}

func TestSlidingWindow_Rate(t *testing.T) {
	window := NewSlidingWindow(1*time.Minute, 1*time.Hour)

	// 添加 10 个事件
	for i := 0; i < 10; i++ {
		window.Add(1)
	}

	// 验证速率
	rate := window.Rate()
	assert.Equal(t, 10.0, rate, "每小时速率应该是 10")
}

func TestSlidingWindow_Average(t *testing.T) {
	window := NewSlidingWindow(1*time.Minute, 1*time.Hour)

	// 添加不同的值
	window.Add(10)
	window.Add(20)
	window.Add(30)

	// 验证平均值
	avg := window.Average()
	assert.Equal(t, 20.0, avg, "平均值应该是 20")
}

func TestSlidingWindow_CleanExpired(t *testing.T) {
	// 使用很小的窗口以便测试过期清理
	window := NewSlidingWindow(100*time.Millisecond, 500*time.Millisecond)

	// 添加一些事件
	window.Add(1)
	window.Add(1)

	assert.Equal(t, 2, window.Count(), "初始应该有 2 个事件")

	// 等待超过窗口大小
	time.Sleep(600 * time.Millisecond)

	// 添加新事件（会触发清理）
	window.Add(1)

	// 旧事件应该被清理
	count := window.Count()
	assert.Equal(t, 1, count, "过期事件应该被清理")
}

func TestSlidingWindow_EmptyWindow(t *testing.T) {
	window := NewSlidingWindow(1*time.Minute, 1*time.Hour)

	// 空窗口
	assert.Equal(t, 0, window.Count())
	assert.Equal(t, 0.0, window.Rate())
	assert.Equal(t, 0.0, window.Average())
}

func TestSlidingWindow_MaxBuckets(t *testing.T) {
	// 1 分钟桶，5 分钟窗口 = 最多 5 个桶
	window := NewSlidingWindow(1*time.Minute, 5*time.Minute)

	// 验证 maxBuckets
	assert.Equal(t, 5, window.maxBuckets)
}

func TestSlidingWindow_ConcurrentAdd(t *testing.T) {
	window := NewSlidingWindow(1*time.Minute, 1*time.Hour)

	// 并发添加
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				window.Add(1)
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证总数
	count := window.Count()
	assert.Equal(t, 1000, count, "应该有 1000 个事件")
}
