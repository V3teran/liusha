package planner

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestObjectiveCreation 测试 Objective 节点创建
func TestObjectiveCreation(t *testing.T) {
	ctx := context.Background()

	// Mock graph store
	store := &mockGraphStore{
		nodes: make(map[string]explorationgraph.Node),
	}

	// 创建 Objective 节点
	openState := explorationgraph.StateOpen
	node := explorationgraph.Node{
		ID:         "test-objective-1",
		TaskID:     "test-task-1",
		Kind:       core.KindObjective,
		State:      &openState, // 必须设置 State
		Content:    []byte(`{"description":"测试目标"}`),
		Priority:   explorationgraph.PriorityMedium,
		SourceType: "user",
		SourceID:   "test",
	}

	// 验证创建成功
	_, err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("Objective 创建失败: %v", err)
	}

	// 验证 State 字段
	if node.State == nil {
		t.Error("Objective 节点缺少 State 字段")
	}

	// 验证可以读取
	objectives, err := store.ListNodesByKind(ctx, "test-task-1", core.KindObjective)
	if err != nil {
		t.Fatalf("读取 Objective 失败: %v", err)
	}

	if len(objectives) != 1 {
		t.Errorf("期望 1 个 Objective，实际 %d 个", len(objectives))
	}
}

// TestDependencyFiltering 测试依赖过滤（调用 intelligence.go 的真实实现）
func TestDependencyFiltering(t *testing.T) {
	validUUID := "12345678-1234-1234-1234-123456789012"
	anotherUUID := "aabbccdd-1234-1234-1234-123456789012"

	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "过滤非 UUID 的编造依赖",
			input:    []string{"0", validUUID},
			expected: []string{validUUID},
		},
		{
			name:     "过滤短依赖",
			input:    []string{"short", validUUID},
			expected: []string{validUUID},
		},
		{
			name:     "全部有效",
			input:    []string{validUUID, anotherUUID},
			expected: []string{validUUID, anotherUUID},
		},
		{
			name:     "全部无效",
			input:    []string{"0", "short", "valid-uuid-1234"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterValidDependencies(tt.input, nil)
			if len(result) != len(tt.expected) {
				t.Errorf("期望 %d 个依赖，实际 %d 个: %v", len(tt.expected), len(result), result)
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("第 %d 个依赖不一致：期望 %s，实际 %s", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

// 幻影依赖（e2e 实测死锁根因）：格式合法的 UUID 但图中不存在——LLM 照抄 prompt
// 示例所致。存在性校验必须把它滤掉，否则 Action 永久 blocked、全图死锁。
func TestDependencyFiltering_PhantomIDs(t *testing.T) {
	realID := "11111111-2222-3333-4444-555555555555"
	phantom := "0aa429b8-1803-42b1-afcb-4300304857d6" // 曾是 prompt 示例 UUID

	existing := map[string]bool{realID: true}
	got := filterValidDependencies([]string{phantom, realID}, existing)
	if len(got) != 1 || got[0] != realID {
		t.Fatalf("幻影依赖应被滤掉，仅留图中存在的 ID: got %v", got)
	}
}

// Mock implementation
type mockGraphStore struct {
	nodes map[string]explorationgraph.Node
}

func (m *mockGraphStore) CreateNode(_ context.Context, node explorationgraph.Node) (explorationgraph.Node, error) {
	// 模拟数据库约束 ck_state_by_kind：objective 节点必须有 state
	if node.Kind == core.KindObjective && node.State == nil {
		return explorationgraph.Node{}, errors.New("ck_state_by_kind: objective 节点必须有 state 字段")
	}

	m.nodes[node.ID] = node
	return node, nil
}

func (m *mockGraphStore) ListNodesByKind(_ context.Context, taskID string, kind core.NodeKind) ([]explorationgraph.Node, error) {
	var result []explorationgraph.Node
	for _, node := range m.nodes {
		if node.TaskID == taskID && node.Kind == kind {
			result = append(result, node)
		}
	}
	return result, nil
}
