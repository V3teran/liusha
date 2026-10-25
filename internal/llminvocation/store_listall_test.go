// +build integration

package llminvocation

import (
	"context"
	"testing"
)

// TestListAll_EmptySearch 验证：空搜索能返回所有数据
func TestListAll_EmptySearch(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	// 插入测试数据
	_, err := s.Append(ctx, Invocation{
		TaskID:    &taskID,
		Provider:  "test",
		Model:     "test-model",
		InTokens:  100,
		OutTokens: 50,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// 空搜索应该返回所有数据
	rows, err := s.ListAll(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("ListAll with empty search: %v", err)
	}
	if len(rows) == 0 {
		t.Error("expected at least one row, got 0")
	}

	// 带分页
	rows2, err := s.ListAll(ctx, ListFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAll with pagination: %v", err)
	}
	if len(rows2) == 0 {
		t.Error("expected at least one row with pagination, got 0")
	}
}
