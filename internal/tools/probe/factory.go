package probe

import (
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/replay"
)

// Factory 把 BAC 4 个 action 的共同依赖（creds / flows / replay engine）打包，
// 每次 CreateActions / Register 都会新建一个独立 *Session，
// 保证不同 BAC task / engagement 之间的状态严格隔离。
type Factory struct {
	creds  credential.Provider
	flows  FlowReader
	replay *replay.Engine
}

// NewFactory 构造 Factory；任一依赖不应为 nil（调用点 main 装配时校验）。
func NewFactory(creds credential.Provider, flows FlowReader, eng *replay.Engine) *Factory {
	return &Factory{creds: creds, flows: flows, replay: eng}
}

// CreateActions 为一个 BAC task 创建 4 个共享同一 *Session 的 action。
//
// engagementID 当前不参与构造，只作 future tagging / 日志锚点（保留给 plan T3 的 skill 装配用）；
// 不影响 action 行为。
func (f *Factory) CreateActions(_ string) []tool.Action {
	session := &Session{}
	return []tool.Action{
		&FetchCredentials{Provider: f.creds, Session: session},
		&ReplayMultiIdentity{Engine: f.replay, Flows: f.flows, Session: session},
		&HeuristicCheck{Session: session},
		&ComputeSimilarity{Session: session},
	}
}

// Register 把 CreateActions 产物全部注册进给定 Registry；任一 Register 失败立即返回。
//
// 用于 BAC skill 装配阶段（plan 2 T3 会调用）：
//
//	reg := tool.NewRegistry()
//	if err := bacFactory.Register(reg, eng.ID); err != nil { ... }
func (f *Factory) Register(reg *tool.Registry, engagementID string) error {
	for _, a := range f.CreateActions(engagementID) {
		if err := reg.Register(a); err != nil {
			return fmt.Errorf("注册 BAC action %q 失败: %w", a.Name(), err)
		}
	}
	return nil
}
