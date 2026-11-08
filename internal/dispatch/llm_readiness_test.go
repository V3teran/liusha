package dispatch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 下发边界的 LLM 就绪校验：任务在创建前必须可被服务——否则任务入队后 planner
// 拿到「未配置」错误无限重试、永挂 active（零 provider/无路由/密钥缺失同属此类）。
// 校验语义由 cmd/api 的适配器实现（Router.For(simple)——simple 是全部档位的降级
// 兜底，它可解析 ⟺ 任意档位可解析）；这里锁定 guard 的判定与错误链。

type fakeReadiness struct{ err error }

func (f fakeReadiness) Ready(context.Context) error { return f.err }

func TestRequireLLMReady(t *testing.T) {
	sentinel := fmt.Errorf("wrap: %w", llm.ErrNotConfigured)
	s := &Service{LLM: fakeReadiness{err: sentinel}}

	err := s.requireLLMReady(context.Background())
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("未就绪错误应透传哨兵链，得 %v", err)
	}

	s.LLM = fakeReadiness{err: nil}
	if err := s.requireLLMReady(context.Background()); err != nil {
		t.Fatalf("就绪时应放行，得 %v", err)
	}

	s.LLM = nil // 未装配（旧测试替身/纯存储用法）→ 不拦截
	if err := s.requireLLMReady(context.Background()); err != nil {
		t.Fatalf("nil LLM 应跳过校验，得 %v", err)
	}
}
