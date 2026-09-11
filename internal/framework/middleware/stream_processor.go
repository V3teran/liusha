package middleware

import (
	"context"
	"sync"
)

// StreamProcessor 是流式处理器，支持转换和过滤。
type StreamProcessor struct {
	transformers []StreamTransformer
	filters      []StreamFilter
	mu           sync.RWMutex
}

// StreamTransformer 是流式转换器。
type StreamTransformer func(context.Context, StreamEvent) (StreamEvent, error)

// StreamFilter 是流式过滤器（返回 true 表示保留）。
type StreamFilter func(StreamEvent) bool

// NewStreamProcessor 创建流式处理器。
func NewStreamProcessor() *StreamProcessor {
	return &StreamProcessor{
		transformers: make([]StreamTransformer, 0),
		filters:      make([]StreamFilter, 0),
	}
}

// Transform 添加转换器。
func (p *StreamProcessor) Transform(transformer StreamTransformer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transformers = append(p.transformers, transformer)
}

// Filter 添加过滤器。
func (p *StreamProcessor) Filter(filter StreamFilter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.filters = append(p.filters, filter)
}

// Process 处理流式事件。
func (p *StreamProcessor) Process(ctx context.Context, event StreamEvent) (StreamEvent, bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// 应用过滤器
	for _, filter := range p.filters {
		if !filter(event) {
			return event, false, nil // 过滤掉
		}
	}

	// 应用转换器
	var err error
	for _, transformer := range p.transformers {
		event, err = transformer(ctx, event)
		if err != nil {
			return event, false, err
		}
	}

	return event, true, nil
}

// ProcessStream 处理整个流。
func (p *StreamProcessor) ProcessStream(ctx context.Context, input <-chan StreamEvent) <-chan StreamEvent {
	output := make(chan StreamEvent, cap(input))

	go func() {
		defer close(output)

		for {
			select {
			case event, ok := <-input:
				if !ok {
					return // 输入流关闭
				}

				// 处理事件
				processed, keep, err := p.Process(ctx, event)
				if err != nil {
					// 错误处理：可以选择跳过或停止
					continue
				}

				if keep {
					select {
					case output <- processed:
					case <-ctx.Done():
						return
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return output
}

// ============================================
// 内置转换器
// ============================================

// CompressTransformer 压缩事件（合并连续的相同类型事件）。
func CompressTransformer() StreamTransformer {
	var lastEvent *StreamEvent

	return func(ctx context.Context, event StreamEvent) (StreamEvent, error) {
		// 简化版：仅合并连续的 LLM 流式输出
		if event.Type == "llm.stream" && lastEvent != nil && lastEvent.Type == "llm.stream" {
			// 合并 Delta
			if lastData, ok := lastEvent.Data.(LLMStreamEvent); ok {
				if currData, ok := event.Data.(LLMStreamEvent); ok {
					currData.Accumulated = lastData.Accumulated + currData.Delta
					event.Data = currData
				}
			}
		}

		lastEvent = &event
		return event, nil
	}
}

// EnrichTransformer 丰富事件（添加额外信息）。
func EnrichTransformer(enricher func(StreamEvent) StreamEvent) StreamTransformer {
	return func(ctx context.Context, event StreamEvent) (StreamEvent, error) {
		return enricher(event), nil
	}
}

// ThrottleTransformer 限流转换器（跳过过于频繁的事件）。
func ThrottleTransformer(maxEventsPerSec int) StreamTransformer {
	// 简化实现：实际需要使用 rate limiter
	return func(ctx context.Context, event StreamEvent) (StreamEvent, error) {
		return event, nil
	}
}

// ============================================
// 内置过滤器
// ============================================

// TypeFilter 按类型过滤。
func TypeFilter(allowedTypes ...string) StreamFilter {
	typeMap := make(map[string]bool)
	for _, t := range allowedTypes {
		typeMap[t] = true
	}

	return func(event StreamEvent) bool {
		return typeMap[event.Type]
	}
}

// PriorityFilter 按优先级过滤（仅保留高于指定优先级的事件）。
func PriorityFilter(minPriority int) StreamFilter {
	return func(event StreamEvent) bool {
		// 假设 Data 中有 priority 字段
		if data, ok := event.Data.(map[string]any); ok {
			if priority, ok := data["priority"].(int); ok {
				return priority >= minPriority
			}
		}
		return true // 默认保留
	}
}

// SamplingFilter 采样过滤器（每 N 个事件保留 1 个）。
func SamplingFilter(rate int) StreamFilter {
	count := 0

	return func(event StreamEvent) bool {
		count++
		return count%rate == 0
	}
}

// ============================================
// 组合处理器
// ============================================

// ChainProcessors 链式组合多个处理器。
func ChainProcessors(processors ...*StreamProcessor) *StreamProcessor {
	combined := NewStreamProcessor()

	for _, p := range processors {
		p.mu.RLock()
		combined.transformers = append(combined.transformers, p.transformers...)
		combined.filters = append(combined.filters, p.filters...)
		p.mu.RUnlock()
	}

	return combined
}
