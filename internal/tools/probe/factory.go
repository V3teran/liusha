package probe

import (
	"fmt"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolfx"
)

// Factory 把 BAC 4 个 action 的共同依赖（creds / flows / replay engine）打包，
// 每次 CreateActions / Register 都会新建一个独立 *ProbeState，
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

// CreateActions 为一个 BAC task 创建 4 个共享同一 *ProbeState 的 action。
//
// engagementID 当前不参与构造，只作 future tagging / 日志锚点（保留给 plan T3 的 skill 装配用）；
// 不影响 action 行为。
//
// locations 来自上游 classify_traffic 输出（经 delegate 透传），用于 FetchCredentials
// 构造带占位 token 的 anonymous 假认证；为空时 anonymous 退化为"完全无凭证"。
func (f *Factory) CreateActions(_ string, locations []credential.CredentialLocation) []toolfx.Action {
	state := &ProbeState{}
	return []toolfx.Action{
		&FetchCredentials{Provider: f.creds, State: state, Locations: locations},
		&ReplayMultiIdentity{Engine: f.replay, Flows: f.flows, State: state},
		&HeuristicCheck{State: state},
		&ComputeSimilarity{State: state},
	}
}

// Register 把 CreateActions 产物全部注册进给定 Registry；任一 Register 失败立即返回。
//
// 用于 BAC skill 装配阶段：
//
//	reg := toolfx.NewRegistry()
//	if err := bacFactory.Register(reg, eng.ID, params.CredentialLocations); err != nil { ... }
func (f *Factory) Register(reg *toolfx.Registry, engagementID string, locations []credential.CredentialLocation) error {
	for _, a := range f.CreateActions(engagementID, locations) {
		if err := reg.Register(a); err != nil {
			return fmt.Errorf("注册 BAC action %q 失败: %w", a.Name(), err)
		}
	}
	return nil
}
