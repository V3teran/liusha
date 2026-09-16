package registry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAgent 是测试用 Mock Agent
type mockAgent struct {
	name string
	typ  string
}

func (m *mockAgent) Name() string                               { return m.name }
func (m *mockAgent) Run(ctx context.Context) error              { return nil }
func (m *mockAgent) Stop(ctx context.Context) error             { return nil }
func (m *mockAgent) ExportState() (json.RawMessage, error)      { return nil, nil }
func (m *mockAgent) ImportState(data json.RawMessage) error     { return nil }

// mockBuilder 创建 Mock Agent
func mockBuilder(agentType string) AgentBuilder {
	return func(config core.AgentConfig) (core.Agent, error) {
		return &mockAgent{
			name: config.Name,
			typ:  agentType,
		}, nil
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	t.Run("successful registration", func(t *testing.T) {
		err := r.Register("planner", mockBuilder("planner"))
		require.NoError(t, err)

		assert.True(t, r.IsRegistered("planner"))
	})

	t.Run("duplicate registration", func(t *testing.T) {
		err := r.Register("planner", mockBuilder("planner"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already registered")
	})

	t.Run("empty agent type", func(t *testing.T) {
		err := r.Register("", mockBuilder("test"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("nil builder", func(t *testing.T) {
		err := r.Register("test", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})
}

func TestRegistry_MustRegister(t *testing.T) {
	r := NewRegistry()

	t.Run("successful registration", func(t *testing.T) {
		assert.NotPanics(t, func() {
			r.MustRegister("executor", mockBuilder("executor"))
		})
	})

	t.Run("panic on duplicate", func(t *testing.T) {
		assert.Panics(t, func() {
			r.MustRegister("executor", mockBuilder("executor"))
		})
	})
}

func TestRegistry_CreateAgent(t *testing.T) {
	r := NewRegistry()
	r.MustRegister("planner", mockBuilder("planner"))

	t.Run("create registered agent", func(t *testing.T) {
		config := core.AgentConfig{
			Name: "test-planner",
			Type: "planner",
		}

		agent, err := r.CreateAgent(config)
		require.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, "test-planner", agent.Name())
	})

	t.Run("create unregistered agent", func(t *testing.T) {
		config := core.AgentConfig{
			Name: "test-unknown",
			Type: "unknown",
		}

		_, err := r.CreateAgent(config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not registered")
	})
}

func TestRegistry_RegisteredTypes(t *testing.T) {
	r := NewRegistry()

	r.MustRegister("planner", mockBuilder("planner"))
	r.MustRegister("executor", mockBuilder("executor"))
	r.MustRegister("monitor", mockBuilder("monitor"))

	types := r.RegisteredTypes()
	assert.Len(t, types, 3)
	assert.Contains(t, types, "planner")
	assert.Contains(t, types, "executor")
	assert.Contains(t, types, "monitor")
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()

	r.MustRegister("planner", mockBuilder("planner"))
	assert.True(t, r.IsRegistered("planner"))

	r.Unregister("planner")
	assert.False(t, r.IsRegistered("planner"))
}

func TestRegistry_Clear(t *testing.T) {
	r := NewRegistry()

	r.MustRegister("planner", mockBuilder("planner"))
	r.MustRegister("executor", mockBuilder("executor"))

	assert.Len(t, r.RegisteredTypes(), 2)

	r.Clear()
	assert.Len(t, r.RegisteredTypes(), 0)
}

func TestGlobalRegistry(t *testing.T) {
	// 清理全局注册表
	defer Global().Clear()

	t.Run("register globally", func(t *testing.T) {
		err := Register("planner", mockBuilder("planner"))
		require.NoError(t, err)

		assert.True(t, IsRegistered("planner"))
	})

	t.Run("create from global", func(t *testing.T) {
		config := core.AgentConfig{
			Name: "global-planner",
			Type: "planner",
		}

		agent, err := CreateAgent(config)
		require.NoError(t, err)
		assert.Equal(t, "global-planner", agent.Name())
	})

	t.Run("list global types", func(t *testing.T) {
		types := RegisteredTypes()
		assert.Contains(t, types, "planner")
	})
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()

	// 并发注册
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			agentType := string(rune('a' + n))
			_ = r.Register(agentType, mockBuilder(agentType))
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证并发安全
	types := r.RegisteredTypes()
	assert.GreaterOrEqual(t, len(types), 1)
}

func TestRegistry_IntegrationWithOrchestrator(t *testing.T) {
	// 集成测试：Registry + Orchestrator
	r := NewRegistry()

	// 注册业务 Agents
	r.MustRegister("planner", func(config core.AgentConfig) (core.Agent, error) {
		return &mockAgent{name: config.Name, typ: "planner"}, nil
	})

	r.MustRegister("executor", func(config core.AgentConfig) (core.Agent, error) {
		return &mockAgent{name: config.Name, typ: "executor"}, nil
	})

	// 创建工作流配置
	workflowConfig := &orchestrator.WorkflowConfig{
		Name: "test_workflow",
		Agents: []core.AgentConfig{
			{Name: "planner1", Type: "planner"},
			{Name: "executor1", Type: "executor"},
		},
		Workflow: []orchestrator.StepConfig{
			{Name: "plan", Agent: "planner1", Output: "plan_result"},
			{Name: "execute", Agent: "executor1", Output: "execute_result"},
		},
	}

	// 使用 Registry 作为 AgentFactory
	runner, err := orchestrator.NewRunner(workflowConfig, r)
	require.NoError(t, err)

	// 执行工作流
	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 验证执行结果
	outputs := runner.GetAllOutputs()
	assert.Len(t, outputs, 2)

	t.Log("✅ Registry + Orchestrator integration successful")
}
