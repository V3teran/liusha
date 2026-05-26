package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/endpoint"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// EndpointReader 是 ReadEndpoints 工具依赖的最小接口。
// *endpoint.Store 自动满足。
type EndpointReader interface {
	ListByOwner(ctx context.Context, ownerID, host string) ([]endpoint.Endpoint, error)
}

// ReadEndpoints — commander/striker 读取本 (owner, host) 攻击面注册表。
//
// commander 用途（核心）：
//   - 持续思考阶段：对整个渗透测试任务有全面体感（哪些攻击面已识别）
//   - done 前自检（**striker 全 done 后必再读一次**）：对照 list_strikers brief 历史判断哪些 endpoint 还没派 striker
//
// striker 用途：
//   - 自查 brief 是否已被 commander/同辈 striker 覆盖
//   - 看相邻 endpoint 找 chaining 线索
//
// 设计反思（0057）：endpoint 表删 status 字段后，本工具仅返完整列表；
// 是否"已挖"由 commander 综合 list_strikers brief 历史 + read_findings 做（context 相关）。
// 命名复数跟 read_findings / read_lessons / read_notes 一致。
type ReadEndpoints struct {
	Store   EndpointReader
	OwnerID string
	Host    string
}

// Name 返回工具名 "read_endpoints"。
func (a *ReadEndpoints) Name() string { return "read_endpoints" }

func (a *ReadEndpoints) Description() string {
	return "读取本 (owner, host) 范围的 endpoint 列表（attack surface 注册表）。" +
		"\n\n【返回】每个 endpoint: {method, path, discovered_at}。" +
		"\n【何时用】" +
		"\n- commander 持续思考：对攻击面有全局体感，决定 spawn 哪个 chaining striker / 是否补 spawn 漏的 endpoint" +
		"\n- commander done 前自检（**striker 全 done 后必再读一次**）：对照 list_strikers brief 历史看是否所有 endpoint 都派过 striker" +
		"\n- striker 自查：本 brief 范围是否已被 commander/同辈覆盖（dedup 防重复挖）" +
		"\n\n【设计要点】endpoint 表不带状态字段——同一 endpoint 在不同 context（凭证 / 已知 finding）下挖洞结果不同，状态字段会阻止合理的 chaining re-spawn。commander 用 list_strikers brief + read_findings 综合判断是否已挖。"
}

// ParametersJSON 返回空对象 schema（0057 删 status filter 后无参数）。
func (a *ReadEndpoints) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 拉取本 (owner, host) 全部 endpoint，返回紧凑 JSON。
func (a *ReadEndpoints) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	endpoints, err := a.Store.ListByOwner(ctx, a.OwnerID, a.Host)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 endpoint 失败: %w", err)
	}

	// 紧凑输出：仅 LLM 关心的字段（id / owner_id / host 省略，避免 token 浪费）
	out := make([]map[string]any, 0, len(endpoints))
	for _, ep := range endpoints {
		row := map[string]any{
			"method":        ep.Method,
			"path":          ep.Path,
			"discovered_at": ep.DiscoveredAt,
		}
		if ep.Name != "" {
			row["name"] = ep.Name
		}
		out = append(out, row)
	}
	body, _ := json.Marshal(out)
	summary := fmt.Sprintf("read_endpoints count=%d", len(endpoints))
	return toolfx.Result{Output: body, Summary: summary}, nil
}
