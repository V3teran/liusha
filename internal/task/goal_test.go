package task

import (
	"errors"
	"strings"
	"testing"
)

// 目标长度契约：上限以内放行，超出返回可识别的 ErrGoalTooLong（入口据此回 400）。按字符计，不按字节。
func TestValidateGoal(t *testing.T) {
	if err := ValidateGoal(strings.Repeat("目", MaxGoalRunes)); err != nil {
		t.Fatalf("恰好上限应放行: %v", err)
	}
	err := ValidateGoal(strings.Repeat("目", MaxGoalRunes+1))
	if !errors.Is(err, ErrGoalTooLong) {
		t.Fatalf("超出上限应返回 ErrGoalTooLong，得 %v", err)
	}
}
