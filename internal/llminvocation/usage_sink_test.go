package llminvocation

import (
	"fmt"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 排不到账户的在途名额，是本地为账户限流做的节流：审计里与限流同列，不记成其它错误。
func TestErrorKindOf_InflightBusyIsRateLimited(t *testing.T) {
	err := fmt.Errorf("%w（上限 4）：排不到名额，本次调用未发出", llm.ErrInflightBusy)
	if got := ErrorKindOf(llm.Classify(err)); got != "rate_limited" {
		t.Fatalf("排不到在途名额应记为限流，实际 %q", got)
	}
}
