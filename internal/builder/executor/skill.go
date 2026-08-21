// Package operator 提供 operator agent 的 prompt 资产（system prompt 分段 + user prompt 拼装）+ Deps。
//
// 本包提供 prompt-as-code 资产，供 cmd/runner 路径复用：
//   - SystemPromptFor / SystemPrompt：system prompt 分段（公共底座 + 角色段）
//   - BuildUserPrompt：流量 / finding / 情报黑板(lead) / 工具索引 段的统一拼装
//   - Deps：prompt 拼装所需的 store / loader / manifest 依赖
//
// 注：跨目标知识库 corpus 是 PULL（search_corpus 工具，agent 按需检索），不在此 PUSH 注入。
//
// 本包只含 prompt-as-code 资产；agent 编排装配在 dispatcher/actor 层。
package operator

import (
	"context"
	_ "embed"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// hunter agent system prompt = 公共底座（编译期嵌入）+ 角色 charter（外部 hunters/ 目录）。
//   - 公共底座：通用规则（角色 / 写 finding 铁律 / 凭证协议 / mode-invariant 反模式），所有角色共用
//   - 角色 charter：active 走 hunters/active/*.md（orchestrator + 杀伤链子代理），
//     passive 走 hunters/passive/traffic-analysis.md——运行期经 cfgStore 加载，不再编译期嵌入。
//
// 历史上 trafficAnalysis / exploitation 段也编译期嵌入（system_prompt_*.md），已并入各自角色 md 退役。
// 改 prompt 走 PR + review，与代码同路径管理（prompt-as-code 实践）。
//
//go:embed system_prompt.md
var operatorSystemPrompt string

// SystemPrompt 导出公共底座。所有角色（orchestrator / 子代理 / traffic-analysis）
// 都用此 + 各自 hunters/ 目录里的角色 charter 组装完整 system prompt。
func SystemPrompt() string {
	return operatorSystemPrompt
}

// BuildUserPrompt 导出 buildUserPrompt，供 runner 路径复用流量/finding/情报黑板/索引段的统一拼装。
func BuildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	return buildUserPrompt(ctx, deps, p)
}

// Deps 是 prompt 拼装依赖注入（由 cmd/runner/main.go 构造一份）。
type Deps struct {
	Findings        *finding.Store
	Credentials     credential.Provider
	Lead            *lead.Store           // user prompt 情报黑板段（§7.5，顶层 agent 只读注入，无工具）
	ToolInvocations *toolinvocation.Store // tool_invocation 遥测落库
	ToolingLoader   *skill.Loader         // read_tooling_skill；nil 不注册
	ToolsManifest   *manifest.Manifest    // user prompt 的工具索引段（tooling_catalog）
	VulnLoader      *skill.Loader         // read_vuln_skill + 漏洞挖掘指南索引段；nil 不注入

	UserPromptBodyLimit int // 请求/响应 body 单段截断字节数；≤0 → 8192
	// FindingsLimit 已移出：改由 BuilderParams.FindingsLimit 每次现读注入（settingstore runtime 组），
	// 不再随 Deps 启动烘焙。见 skill.BuilderParams。
}
