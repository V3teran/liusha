package core_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChanneledState_Overwrite 测试覆盖策略
func TestChanneledState_Overwrite(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("context", core.ChannelOverwrite)

	// 设置初始值
	err := state.Set("context", "initial")
	require.NoError(t, err)

	val, err := state.Get("context")
	require.NoError(t, err)
	assert.Equal(t, "initial", val)

	// 覆盖值
	err = state.Set("context", "updated")
	require.NoError(t, err)

	val, err = state.Get("context")
	require.NoError(t, err)
	assert.Equal(t, "updated", val)
}

// TestChanneledState_Append 测试追加策略
func TestChanneledState_Append(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("messages", core.ChannelAppend)

	// 设置初始值
	err := state.Set("messages", []string{"msg1"})
	require.NoError(t, err)

	// 合并新值
	state2 := core.NewChanneledState()
	state2.DefineChannel("messages", core.ChannelAppend)
	err = state2.Set("messages", []string{"msg2", "msg3"})
	require.NoError(t, err)

	err = state.Merge(state2)
	require.NoError(t, err)

	val, err := state.Get("messages")
	require.NoError(t, err)
	assert.Equal(t, []string{"msg1", "msg2", "msg3"}, val)
}

// TestChanneledState_AppendStrings 测试字符串追加
func TestChanneledState_AppendStrings(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("log", core.ChannelAppend)

	// 设置初始值
	err := state.Set("log", "line1\n")
	require.NoError(t, err)

	// 获取 channel 并追加
	// 注意：需要通过 Merge 方法测试
	state2 := core.NewChanneledState()
	state2.DefineChannel("log", core.ChannelAppend)
	err = state2.Set("log", "line2\n")
	require.NoError(t, err)

	err = state.Merge(state2)
	require.NoError(t, err)

	val, err := state.Get("log")
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", val)
}

// TestChanneledState_CustomReducer 测试自定义 Reducer
func TestChanneledState_CustomReducer(t *testing.T) {
	state := core.NewChanneledState()

	// 自定义 Reducer：计数器累加
	state.DefineCustomChannel("counter", func(old, new interface{}) (interface{}, error) {
		oldVal := 0
		if old != nil {
			oldVal = old.(int)
		}
		newVal := new.(int)
		return oldVal + newVal, nil
	})

	// 设置初始值
	err := state.Set("counter", 10)
	require.NoError(t, err)

	// 合并新值
	state2 := core.NewChanneledState()
	state2.DefineCustomChannel("counter", func(old, new interface{}) (interface{}, error) {
		return new, nil
	})
	err = state2.Set("counter", 5)
	require.NoError(t, err)

	err = state.Merge(state2)
	require.NoError(t, err)

	val, err := state.Get("counter")
	require.NoError(t, err)
	assert.Equal(t, 15, val)
}

// TestChanneledState_MultipleChannels 测试多通道混合
func TestChanneledState_MultipleChannels(t *testing.T) {
	state := core.NewChanneledState()

	// 定义不同策略的通道
	state.DefineChannel("messages", core.ChannelAppend)   // 追加
	state.DefineChannel("context", core.ChannelOverwrite) // 覆盖
	state.DefineChannel("metadata", core.ChannelOverwrite)

	// 设置值
	err := state.Set("messages", []string{"hello"})
	require.NoError(t, err)

	err = state.Set("context", "initial context")
	require.NoError(t, err)

	err = state.Set("metadata", map[string]string{"version": "1.0"})
	require.NoError(t, err)

	// 合并新状态
	state2 := core.NewChanneledState()
	state2.DefineChannel("messages", core.ChannelAppend)
	state2.DefineChannel("context", core.ChannelOverwrite)

	err = state2.Set("messages", []string{"world"})
	require.NoError(t, err)

	err = state2.Set("context", "updated context")
	require.NoError(t, err)

	err = state.Merge(state2)
	require.NoError(t, err)

	// 验证结果
	messages, err := state.Get("messages")
	require.NoError(t, err)
	assert.Equal(t, []string{"hello", "world"}, messages)

	context, err := state.Get("context")
	require.NoError(t, err)
	assert.Equal(t, "updated context", context)

	metadata, err := state.Get("metadata")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"version": "1.0"}, metadata)
}

// TestChanneledState_Clone 测试克隆
func TestChanneledState_Clone(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("data", core.ChannelOverwrite)
	err := state.Set("data", "original")
	require.NoError(t, err)

	// 克隆
	cloned := state.Clone()

	// 修改原状态
	err = state.Set("data", "modified")
	require.NoError(t, err)

	// 验证克隆未受影响
	val, err := cloned.Get("data")
	require.NoError(t, err)
	assert.Equal(t, "original", val)
}

// TestChanneledState_UndefinedChannel 测试未定义通道
func TestChanneledState_UndefinedChannel(t *testing.T) {
	state := core.NewChanneledState()

	// 尝试访问未定义通道
	_, err := state.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not defined")

	err = state.Set("nonexistent", "value")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not defined")
}

// TestChanneledState_HasChannel 测试通道存在性检查
func TestChanneledState_HasChannel(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("exists", core.ChannelOverwrite)

	assert.True(t, state.HasChannel("exists"))
	assert.False(t, state.HasChannel("not_exists"))
}

// TestChanneledState_GetChannelNames 测试获取通道名称
func TestChanneledState_GetChannelNames(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("channel1", core.ChannelOverwrite)
	state.DefineChannel("channel2", core.ChannelAppend)
	state.DefineChannel("channel3", core.ChannelOverwrite)

	names := state.GetChannelNames()
	assert.Len(t, names, 3)
	assert.Contains(t, names, "channel1")
	assert.Contains(t, names, "channel2")
	assert.Contains(t, names, "channel3")
}

// TestChanneledState_AppendIntegers 测试整数追加
func TestChanneledState_AppendIntegers(t *testing.T) {
	state := core.NewChanneledState()
	state.DefineChannel("numbers", core.ChannelAppend)

	// 设置初始值
	err := state.Set("numbers", []int{1, 2, 3})
	require.NoError(t, err)

	// 合并新值
	state2 := core.NewChanneledState()
	state2.DefineChannel("numbers", core.ChannelAppend)
	err = state2.Set("numbers", []int{4, 5})
	require.NoError(t, err)

	err = state.Merge(state2)
	require.NoError(t, err)

	val, err := state.Get("numbers")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, val)
}
