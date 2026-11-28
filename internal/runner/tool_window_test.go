package runner

import (
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 工具结果窗口的配置上限不超过单条消息的硬上限：窗口之内的结果连说明在内不超过窗口，所以顶格的
// 窗口也装得进长度哨兵，哨兵永远不会拦下一条过了窗口的结果。config 不引用运行时，这层关系在这里守。
func TestToolOutputWindowLimitFitsMessageLimit(t *testing.T) {
	if config.MaxToolOutputWindowBytes > runtime.MaxMessageBytes {
		t.Fatalf("工具结果窗口上限 %d 超过单条消息的硬上限 %d", config.MaxToolOutputWindowBytes, runtime.MaxMessageBytes)
	}
}
