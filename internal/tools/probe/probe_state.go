// Package probe 提供边界类漏洞主动探针工具集（4 个 action + Factory），
// 被各 vuln skill（BAC、未来 IDOR/认证绕过/SSRF…）共享：
// fetch_credentials / replay_matrix / heuristic_check / compute_similarity。
//
// 命名说明（业界最佳实践）：
//   - "probe" = 主动发请求测试目标（OWASP Active Scanner 标准术语）
//   - 不叫 "sniffer"（被动抓包语义错位）
//   - 跟 LLM RouteKey "hunter"（执行 probe 的漏洞猎手 = 子 ReAct）形成对应
//
// 设计要点：
//   - 4 个 action 通过共享 *ProbeState 在 task 内部传递"上一次的 responses"，
//     避免让 LLM 在每一步重复读取/序列化全部 replay body。
//   - 任何 action 不返回 raw body：ReplayMatrix 只回 body_hint（≤400 byte）；
//     完整 body 留在 *ProbeState 内供后续 heuristic / similarity 使用。
//   - Factory.CreateActions 每次调用产生独立 *ProbeState，按 engagement / task 隔离。
//   - skill 差异在判定逻辑（SKILL.md prompt + builder 装配的 done_validator），探针工具复用。
package probe

import (
	"context"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
)

// OriginalIdentityName 是 replay.OriginalIdentityName 在本包的别名，
// 让 probe 包内代码就近引用，不必再 import replay。
const OriginalIdentityName = replay.OriginalIdentityName

// ProbeState 是一个 BAC task 内 4 个 action 共享的本地状态本：
//   - Identities：FetchCredentials 写入；ReplayMatrix 读取。
//   - LastResponses + LastFlow：ReplayMatrix 写入；
//     HeuristicCheck / ComputeSimilarity 读取。
//
// 命名避开 "Session"（与 HTTP session/credential 概念重叠易误读）。
// ProbeState 不做并发保护：BAC ReAct 循环里 action 串行执行（一次工具调用一个）。
type ProbeState struct {
	Identities    []credential.Identity
	LastResponses []replay.Response
	LastFlow      flow.Flow
}

// AllResponses 返回"原始抓包响应 + 多身份重放响应"的拼接列表，
// 启发式 / 相似度工具读这个，让原始响应作为对比锚点（ground truth）。
//
// 行为：
//   - LastFlow 不存在（ID==0）或没有 ResponseBody → 退化返回 LastResponses；
//   - 否则在切片头部插入一条 IdentityName=_original_ 的伪响应。
//
// 不修改 ProbeState 持有的切片：每次调用都返回新切片，避免别名 bug。
func (s *ProbeState) AllResponses() []replay.Response {
	if s.LastFlow.ID == 0 || len(s.LastFlow.ResponseBody) == 0 {
		return s.LastResponses
	}
	baseline := replay.Response{
		IdentityName: OriginalIdentityName,
		StatusCode:   s.LastFlow.StatusCode,
		Body:         s.LastFlow.ResponseBody,
	}
	out := make([]replay.Response, 0, len(s.LastResponses)+1)
	out = append(out, baseline)
	out = append(out, s.LastResponses...)
	return out
}

// FlowReader 是 BAC 依赖的最小 flow 读接口，由 *flow.Store 自动满足。
// 局部定义在 consumer 侧（Go idiom: accept interfaces, return structs）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}
