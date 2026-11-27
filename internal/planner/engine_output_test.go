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
