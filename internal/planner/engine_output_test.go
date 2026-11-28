package planner

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 规划终稿的验收就是解析器本身：裸 JSON、json 围栏、说明文字夹着 JSON 都认——模型在 JSON
// 前后写的说明不是不合格。取不出 JSON 对象、或 JSON 里没有 should_continue 的不认。
func TestPlanningOutputCheck(t *testing.T) {
	for answer, ok := range map[string]bool{
		`{"should_continue":true,"actions":[]}`:                                     true,
		"```json\n{\"should_continue\":false,\"reasoning\":\"收口\"}\n```":            true,
		"先摸清对象面，再沿线索深挖。\n\n```json\n{\"should_continue\":true,\"actions\":[]}\n```": true,
		`{"reasoning":"还没想好"}`:                                                      false,
		"无需再规划。":                                                                    false,
		"":                                                                          false,
	} {
		err := planningOutputCheck(answer)
		if ok {
			assert.NoError(t, err, answer)
		} else {
			assert.Error(t, err, answer)
		}
	}
}

// 加急一轮至多一个：多标的只有第一个算，其余撤成普通动作——加急靠稀缺才不被通胀。
func TestParsePlanningResponse_CapsUrgentToOne(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop())

	response, err := e.parsePlanningResponse(`{"should_continue":true,"reasoning":"r","actions":[
		{"instruction":"a","urgent":true},{"instruction":"b","urgent":true},{"instruction":"c"},{"instruction":"d","urgent":true}]}`)
	require.NoError(t, err)
	require.Len(t, response.Actions, 4)
	assert.True(t, response.Actions[0].Urgent)
	for i := 1; i < 4; i++ {
		assert.False(t, response.Actions[i].Urgent, "第 %d 个动作上的加急应被撤掉", i+1)
	}
}

// 缺 should_continue 的 JSON 是解析失败（可重试），不再被读成「停止探索」把任务收掉。
func TestParsePlanningResponse_RequiresShouldContinue(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop())

	_, err := e.parsePlanningResponse(`{"reasoning":"还没想好"}`)
	require.ErrorIs(t, err, ErrPlanOutputParse)

	response, err := e.parsePlanningResponse("先摸清对象面。\n" + `{"should_continue":true,"reasoning":"铺开对象面"}`)
	require.NoError(t, err)
	assert.True(t, response.ShouldContinue)
	assert.Equal(t, "铺开对象面", response.Reasoning)
}
