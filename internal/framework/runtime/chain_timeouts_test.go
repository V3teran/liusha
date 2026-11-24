package runtime

import "testing"

// 蒸馏的时限必须先于修改链的总超时到期：蒸馏慢时走降级路径（裁掉前缀、不留纪要），
// 而不是耗尽链超时、让整步推理报错。
func TestDistillTimeoutExpiresBeforeChainTimeout(t *testing.T) {
	if distillCallTimeout >= ChainTimeout {
		t.Fatalf("蒸馏时限 %v 须早于压缩链超时 %v 到期", distillCallTimeout, ChainTimeout)
	}
}
