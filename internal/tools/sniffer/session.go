// Package sniffer 提供漏洞探针通用工具集（4 个 action + Factory），
// 被各 vuln skill（BAC、未来 SQLi/SSRF/IDOR…）共享：
// fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity。
//
// 设计要点（含黑客松借鉴）：
//   - 4 个 action 通过共享 *Session 在 task 内部传递"上一次的 responses"，
//     避免让 LLM 在每一步重复读取/序列化全部 replay body（同 result_compress 精神）。
//   - 任何 action 不返回 raw body：ReplayMultiIdentity 只回 body_hint（≤400 byte）；
//     完整 body 留在 *Session 内供后续 heuristic / similarity 使用。
//   - Factory.CreateActions 每次调用产生独立 *Session，按 engagement / task 隔离。
//   - skill 差异在判定逻辑（SKILL.md prompt + done_validator），探针工具复用。
package sniffer

import (
	"context"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
)

// Session 是一个 BAC task 内 4 个 action 共享的状态：
//   - Identities：FetchCredentials 写入；ReplayMultiIdentity 读取。
//   - LastResponses + LastFlow：ReplayMultiIdentity 写入；
//     HeuristicCheck / ComputeSimilarity 读取。
//
// Session 不做并发保护：BAC ReAct 循环里 action 串行执行（一次工具调用一个）。
type Session struct {
	Identities    []credential.Identity
	LastResponses []replay.Response
	LastFlow      flow.Flow
}

// FlowReader 是 BAC 依赖的最小 flow 读接口，由 *flow.Store 自动满足。
// 局部定义在 consumer 侧（Go idiom: accept interfaces, return structs）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}
