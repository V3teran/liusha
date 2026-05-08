package probe

import (
	"fmt"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// Factory 把 BAC 4 个 action 的共同依赖（creds / flows / replay engine）打包，
// 每次 CreateActions / Register 都会新建一个独立 *ProbeState，
// 保证不同 task / engagement 之间的状态严格隔离。
type Factory struct {
	creds  credential.Provider
	flows  FlowReader
	replay *replay.Engine
}

// NewFactory 构造 Factory；任一依赖不应为 nil（调用点 main 装配时校验）。
func NewFactory(creds credential.Provider, flows FlowReader, eng *replay.Engine) *Factory {
	return &Factory{creds: creds, flows: flows, replay: eng}
}

// Option 是 CreateActions / Register 的函数式选项，按 skill 类型精挑工具子集。
// 默认（不传 opts）注册全部 4 个 action（BAC 行为）。
type Option func(*registerOpts)

// registerOpts 内部状态，仅在 factory.go 内消费。
type registerOpts struct {
	skipSimilarity bool
	cfg            config.ProbeConfig
	cfgSet         bool
	authKeywords   []string
}

// WithoutSimilarity 跳过 ComputeSimilarity 注册。
// 适用：SKILL 不依赖响应相似度差分（如 SQLi——由 sqlmap 容器化验证替代）。
func WithoutSimilarity() Option {
	return func(o *registerOpts) { o.skipSimilarity = true }
}

// WithConfig 把 cfg.Probe 注入到 ReplayMatrix / ComputeSimilarity 的可选字段。
// 零值字段由两个 action 内部 fallback 兜底。
func WithConfig(cfg config.ProbeConfig) Option {
	return func(o *registerOpts) {
		o.cfg = cfg
		o.cfgSet = true
	}
}

// WithAuthKeywords 把 cfg.Heuristic.AuthKeywords 透传给 HeuristicCheck，
// 让 all_auth_error 规则使用 yaml 自定义关键词清单。
// keywords 为 nil/空时 heuristic.AllAuthError 内部回退 heuristic.DefaultAuthKeywords。
func WithAuthKeywords(keywords []string) Option {
	return func(o *registerOpts) {
		o.authKeywords = keywords
	}
}

// CreateActions 为一个 task 创建共享同一 *ProbeState 的 action 列表。
//
// 默认返回 4 个（fetch_credentials / run_replay / check_heuristics / compute_similarity）；
// 传 opts 可裁剪。fetch_credentials 与 run_replay 是核心永远注册——
// 删除其中任一会导致下游工具无 state 可读。
//
// engagementID 当前不参与构造，只作 future tagging / 日志锚点；不影响 action 行为。
//
// locations 来自上游 classify_traffic 输出（经 delegate 透传），用于 FetchCredentials
// 构造带占位 token 的 anonymous 假认证；为空时 anonymous 退化为"完全无凭证"。
func (f *Factory) CreateActions(_ string, locations []credential.CredentialLocation, opts ...Option) []toolfx.Action {
	o := registerOpts{}
	for _, opt := range opts {
		opt(&o)
	}
	state := &ProbeState{}
	replayer := &ReplayMatrix{Engine: f.replay, Flows: f.flows, State: state}
	if o.cfgSet {
		replayer.BodyHintMaxBytes = o.cfg.BodyHintMaxBytes
		replayer.DefaultConcurrency = o.cfg.DefaultConcurrency
	}
	actions := []toolfx.Action{
		&FetchCredentials{Provider: f.creds, State: state, Locations: locations},
		replayer,
	}
	actions = append(actions, &HeuristicCheck{State: state, AuthKeywords: o.authKeywords})
	if !o.skipSimilarity {
		sim := &ComputeSimilarity{State: state}
		if o.cfgSet {
			sim.MinThreshold = o.cfg.SimilarityMinThreshold
			sim.HighThreshold = o.cfg.SimilarityHighThreshold
			sim.LengthRatioGate = o.cfg.LengthRatioGate
		}
		actions = append(actions, sim)
	}
	return actions
}

// Register 把 CreateActions 产物注册进给定 Registry；任一 Register 失败立即返回。
//
// 用于 skill 装配阶段：
//
//	reg := toolfx.NewRegistry()
//	// BAC：注册全部 4 个
//	if err := bacFactory.Register(reg, eng.ID, params.CredentialLocations); err != nil { ... }
//	// SQLi：跳过 similarity（由 sqlmap 替代）
//	if err := sqliFactory.Register(reg, eng.ID, params.CredentialLocations,
//	    probe.WithoutSimilarity()); err != nil { ... }
func (f *Factory) Register(reg *toolfx.Registry, engagementID string, locations []credential.CredentialLocation, opts ...Option) error {
	for _, a := range f.CreateActions(engagementID, locations, opts...) {
		if err := reg.Register(a); err != nil {
			return fmt.Errorf("注册 probe action %q 失败: %w", a.Name(), err)
		}
	}
	return nil
}
