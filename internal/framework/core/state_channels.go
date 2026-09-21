package core

import (
	"fmt"
	"sync"
)

// ChannelStrategy 通道合并策略
type ChannelStrategy string

const (
	// ChannelOverwrite 覆盖策略（新值替换旧值）
	ChannelOverwrite ChannelStrategy = "overwrite"

	// ChannelAppend 追加策略（新值追加到旧值）
	ChannelAppend ChannelStrategy = "append"

	// ChannelCustom 自定义策略（使用 Reducer）
	ChannelCustom ChannelStrategy = "custom"
)

// Channel 状态通道（每个通道独立的合并策略）
type Channel struct {
	// 通道名称
	name string

	// 合并策略
	strategy ChannelStrategy

	// 自定义 Reducer（当 strategy 为 Custom 时使用）
	reducer func(old, new interface{}) (interface{}, error)

	// 当前值
	value interface{}

	// 并发保护
	mu sync.RWMutex
}

// NewChannel 创建新通道
func NewChannel(name string, strategy ChannelStrategy) *Channel {
	return &Channel{
		name:     name,
		strategy: strategy,
	}
}

// NewCustomChannel 创建自定义 Reducer 通道
func NewCustomChannel(name string, reducer func(old, new interface{}) (interface{}, error)) *Channel {
	return &Channel{
		name:     name,
		strategy: ChannelCustom,
		reducer:  reducer,
	}
}

// Set 设置通道值
func (c *Channel) Set(value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
}

// Get 获取通道值
func (c *Channel) Get() interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value
}

// Merge 合并新值到通道
func (c *Channel) Merge(newValue interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.strategy {
	case ChannelOverwrite:
		// 覆盖策略：直接替换
		c.value = newValue
		return nil

	case ChannelAppend:
		// 追加策略：需要类型支持
		return c.appendValue(newValue)

	case ChannelCustom:
		// 自定义策略：使用 Reducer
		if c.reducer == nil {
			return fmt.Errorf("custom channel %s has no reducer", c.name)
		}
		merged, err := c.reducer(c.value, newValue)
		if err != nil {
			return fmt.Errorf("channel %s reducer failed: %w", c.name, err)
		}
		c.value = merged
		return nil

	default:
		return fmt.Errorf("unknown channel strategy: %s", c.strategy)
	}
}

// appendValue 追加值（支持 slice 和 string）
func (c *Channel) appendValue(newValue interface{}) error {
	// 如果旧值为 nil，直接设置
	if c.value == nil {
		c.value = newValue
		return nil
	}

	// 尝试追加 slice
	switch old := c.value.(type) {
	case []interface{}:
		switch nv := newValue.(type) {
		case []interface{}:
			c.value = append(old, nv...)
		default:
			c.value = append(old, nv)
		}
		return nil

	case []string:
		switch nv := newValue.(type) {
		case []string:
			c.value = append(old, nv...)
		case string:
			c.value = append(old, nv)
		default:
			return fmt.Errorf("cannot append %T to []string", newValue)
		}
		return nil

	case []int:
		switch nv := newValue.(type) {
		case []int:
			c.value = append(old, nv...)
		case int:
			c.value = append(old, nv)
		default:
			return fmt.Errorf("cannot append %T to []int", newValue)
		}
		return nil

	case string:
		if nv, ok := newValue.(string); ok {
			c.value = old + nv
			return nil
		}
		return fmt.Errorf("cannot append %T to string", newValue)

	default:
		return fmt.Errorf("channel %s: cannot append to type %T", c.name, c.value)
	}
}

// ChanneledState 多通道状态（替代单一 GraphState）
type ChanneledState struct {
	channels map[string]*Channel
	mu       sync.RWMutex
}

// NewChanneledState 创建多通道状态
func NewChanneledState() *ChanneledState {
	return &ChanneledState{
		channels: make(map[string]*Channel),
	}
}

// DefineChannel 定义通道
func (s *ChanneledState) DefineChannel(name string, strategy ChannelStrategy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[name] = NewChannel(name, strategy)
}

// DefineCustomChannel 定义自定义 Reducer 通道
func (s *ChanneledState) DefineCustomChannel(name string, reducer func(old, new interface{}) (interface{}, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[name] = NewCustomChannel(name, reducer)
}

// Set 设置通道值
func (s *ChanneledState) Set(channelName string, value interface{}) error {
	s.mu.RLock()
	channel, exists := s.channels[channelName]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not defined", channelName)
	}

	channel.Set(value)
	return nil
}

// Get 获取通道值
func (s *ChanneledState) Get(channelName string) (interface{}, error) {
	s.mu.RLock()
	channel, exists := s.channels[channelName]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("channel %s not defined", channelName)
	}

	return channel.Get(), nil
}

// Merge 合并另一个 ChanneledState
func (s *ChanneledState) Merge(other *ChanneledState) error {
	if other == nil {
		return nil
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	for name, otherChannel := range other.channels {
		s.mu.RLock()
		channel, exists := s.channels[name]
		s.mu.RUnlock()

		if !exists {
			// 通道不存在，跳过（或者报错？取决于严格程度）
			continue
		}

		// 合并值
		newValue := otherChannel.Get()
		if err := channel.Merge(newValue); err != nil {
			return fmt.Errorf("merge channel %s failed: %w", name, err)
		}
	}

	return nil
}

// Clone 克隆状态
func (s *ChanneledState) Clone() *ChanneledState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := NewChanneledState()
	for name, channel := range s.channels {
		clonedChannel := NewChannel(name, channel.strategy)
		clonedChannel.reducer = channel.reducer
		clonedChannel.Set(channel.Get())
		cloned.channels[name] = clonedChannel
	}

	return cloned
}

// GetChannelNames 获取所有通道名称
func (s *ChanneledState) GetChannelNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.channels))
	for name := range s.channels {
		names = append(names, name)
	}
	return names
}

// HasChannel 检查通道是否存在
func (s *ChanneledState) HasChannel(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.channels[name]
	return exists
}
