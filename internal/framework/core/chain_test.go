package core

import (
	"context"
	"errors"
	"testing"
)

func TestChainFunc(t *testing.T) {
	ctx := context.Background()

	chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
		s := input.(string)
		return s + " 处理完成", nil
	})

	result, err := chain.Run(ctx, "测试")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result != "测试 处理完成" {
		t.Errorf("result = %q, want %q", result, "测试 处理完成")
	}
}

func TestSequenceChain(t *testing.T) {
	ctx := context.Background()

	t.Run("顺序执行", func(t *testing.T) {
		chain1 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) + 1, nil
		})

		chain2 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) * 2, nil
		})

		chain3 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) - 3, nil
		})

		seq := NewSequenceChain(chain1, chain2, chain3)

		// (5 + 1) * 2 - 3 = 9
		result, err := seq.Run(ctx, 5)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != 9 {
			t.Errorf("result = %d, want 9", result)
		}
	})

	t.Run("中间链失败", func(t *testing.T) {
		chain1 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) + 1, nil
		})

		chain2 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return nil, errors.New("失败")
		})

		chain3 := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) * 2, nil
		})

		seq := NewSequenceChain(chain1, chain2, chain3)

		_, err := seq.Run(ctx, 5)
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestTransformChain(t *testing.T) {
	ctx := context.Background()

	chain := NewTransformChain(func(ctx context.Context, input any) (any, error) {
		s := input.(string)
		return len([]rune(s)), nil
	})

	result, err := chain.Run(ctx, "测试文本")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result != 4 {
		t.Errorf("result = %d, want 4", result)
	}
}

func TestRouterChain(t *testing.T) {
	ctx := context.Background()

	t.Run("路由到不同链", func(t *testing.T) {
		router := NewRouterChain(func(ctx context.Context, input any) (string, error) {
			n := input.(int)
			if n > 10 {
				return "large", nil
			}
			return "small", nil
		})

		largeChain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return "大数字", nil
		})

		smallChain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return "小数字", nil
		})

		router.AddRoute("large", largeChain).AddRoute("small", smallChain)

		result1, _ := router.Run(ctx, 15)
		if result1 != "大数字" {
			t.Errorf("result1 = %q, want %q", result1, "大数字")
		}

		result2, _ := router.Run(ctx, 5)
		if result2 != "小数字" {
			t.Errorf("result2 = %q, want %q", result2, "小数字")
		}
	})

	t.Run("使用默认路由", func(t *testing.T) {
		router := NewRouterChain(func(ctx context.Context, input any) (string, error) {
			return "unknown", nil
		})

		defaultChain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return "默认处理", nil
		})

		router.SetDefault(defaultChain)

		result, _ := router.Run(ctx, 42)
		if result != "默认处理" {
			t.Errorf("result = %q, want %q", result, "默认处理")
		}
	})

	t.Run("无匹配且无默认路由", func(t *testing.T) {
		router := NewRouterChain(func(ctx context.Context, input any) (string, error) {
			return "unknown", nil
		})

		_, err := router.Run(ctx, 42)
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestMapChain(t *testing.T) {
	ctx := context.Background()

	t.Run("映射列表", func(t *testing.T) {
		chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) * 2, nil
		})

		mapper := NewMapChain(chain)

		input := []any{1, 2, 3, 4, 5}
		result, err := mapper.Run(ctx, input)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		results := result.([]any)
		expected := []int{2, 4, 6, 8, 10}

		for i, v := range results {
			if v != expected[i] {
				t.Errorf("results[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})

	t.Run("输入类型错误", func(t *testing.T) {
		chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input, nil
		})

		mapper := NewMapChain(chain)

		_, err := mapper.Run(ctx, "not a slice")
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestRetryChain(t *testing.T) {
	ctx := context.Background()

	t.Run("第一次成功", func(t *testing.T) {
		chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return "成功", nil
		})

		retry := NewRetryChain(chain, 3)

		result, err := retry.Run(ctx, nil)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != "成功" {
			t.Errorf("result = %q, want %q", result, "成功")
		}
	})

	t.Run("第二次成功", func(t *testing.T) {
		attempts := 0

		chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			attempts++
			if attempts < 2 {
				return nil, errors.New("失败")
			}
			return "成功", nil
		})

		retry := NewRetryChain(chain, 3)

		result, err := retry.Run(ctx, nil)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != "成功" {
			t.Errorf("result = %q, want %q", result, "成功")
		}

		if attempts != 2 {
			t.Errorf("attempts = %d, want 2", attempts)
		}
	})

	t.Run("重试耗尽", func(t *testing.T) {
		chain := ChainFunc(func(ctx context.Context, input any) (any, error) {
			return nil, errors.New("持续失败")
		})

		retry := NewRetryChain(chain, 2)

		_, err := retry.Run(ctx, nil)
		if err == nil {
			t.Error("期望错误，但成功了")
		}
	})
}

func TestChainBuilder(t *testing.T) {
	ctx := context.Background()

	t.Run("构建顺序链", func(t *testing.T) {
		builder := NewChainBuilder()

		builder.AddFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) + 1, nil
		})

		builder.AddFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) * 2, nil
		})

		chain := builder.Build()

		// (5 + 1) * 2 = 12
		result, err := chain.Run(ctx, 5)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != 12 {
			t.Errorf("result = %d, want 12", result)
		}
	})

	t.Run("空构建器", func(t *testing.T) {
		builder := NewChainBuilder()
		chain := builder.Build()

		result, err := chain.Run(ctx, "测试")
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != "测试" {
			t.Errorf("result = %q, want %q", result, "测试")
		}
	})

	t.Run("单个链", func(t *testing.T) {
		builder := NewChainBuilder()

		builder.AddFunc(func(ctx context.Context, input any) (any, error) {
			return input.(int) * 10, nil
		})

		chain := builder.Build()

		result, err := chain.Run(ctx, 5)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result != 50 {
			t.Errorf("result = %d, want 50", result)
		}
	})
}
