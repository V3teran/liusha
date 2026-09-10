// Package profile 为每个 Complexity 提供预置的 Dispatcher Profile。
//
// 调用方（buildDispatcher）用 Profiles() 批量注册全部 5 个 Profile，
// 也可按需调用单个工厂函数以覆盖 systemPrompt 或 Budget。
package profile

import (
	"github.com/V3teran/liusha/internal/dispatcher"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Profiles 返回全部 5 个 Complexity 的默认 Profile 列表，传入的 systemPrompt
// 作为各 Profile 系统提示的前缀（外部注入场景人设/角色定位）。
func Profiles(systemPrompt string) []dispatcher.Profile {
	return []dispatcher.Profile{
		Trivial(systemPrompt),
		Simple(systemPrompt),
		Moderate(systemPrompt),
		Complex(systemPrompt),
		Extreme(systemPrompt),
	}
}

// Trivial 返回极简任务的 Profile（<5 步）。
// 适用场景：快速查询、简单验证、单次工具调用。
func Trivial(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务复杂度：极简（trivial）

预期执行步数：<5 步
适用场景：快速信息查询、单次工具调用、简单验证

### 工作原则
1. 快速完成，避免过度探索
2. 优先使用现有数据（list_traffic/read_findings）
3. 单次工具调用即可满足目标时，立即执行
4. 完成后立即调用 done

### 可用工具
- list_traffic、view_traffic：查看已有流量
- read_findings：查看已发现漏洞
- write_lead：记录简单观察
- done：任务完成
`

	return dispatcher.Profile{
		Complexity:   knowledgegraph.ComplexityTrivial,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic",
			"read_findings",
			"write_lead",
			"done",
		},
		Budget: executor.Budget{
			MaxSteps:          5,
			MaxTokens:         10000,
			WatchdogSecs:      60,
			CompactionTrigger: 0.70,
		},
		Settle: executor.SettleConfig{
			Threshold:    0.90,
			Directive:    "任务预算耗尽，立即输出当前结果并调用 done。",
			AllowedTools: []string{"done"},
		},
	}
}

// Simple 返回简单任务的 Profile（~10 步）。
// 适用场景：基础信息收集、简单枚举、初步探测。
func Simple(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务复杂度：简单（simple）

预期执行步数：~10 步
适用场景：基础信息收集、简单枚举、初步探测

### 工作流程
1. 先查看已有数据（list_traffic/read_findings）
2. 执行基础枚举或探测
3. 记录发现的线索（write_lead）
4. 完成后调用 done

### 可用工具
- 数据查询：list_traffic、view_traffic、read_findings、read_credentials
- 信息记录：write_lead、write_finding
- 执行工具：run_command（限简单命令）
- 结束：done
`

	return dispatcher.Profile{
		Complexity:   knowledgegraph.ComplexitySimple,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic",
			"read_credentials",
			"read_findings", "write_finding",
			"write_lead",
			"run_command",
			"done",
		},
		Budget: executor.Budget{
			MaxSteps:          15,
			MaxTokens:         30000,
			WatchdogSecs:      180,
			CompactionTrigger: 0.70,
		},
		Settle: executor.SettleConfig{
			Threshold:    0.85,
			Directive:    "预算接近上限，整理已发现的线索并调用 done。",
			AllowedTools: []string{"write_lead", "write_finding", "done"},
		},
	}
}

// Moderate 返回中等任务的 Profile（~30 步）。
// 适用场景：漏洞测试、流量重放、系统性探测。
func Moderate(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务复杂度：中等（moderate）

预期执行步数：~30 步
适用场景：漏洞测试、流量重放、系统性探测

### 工作流程
1. 分析目标和已有信息
2. 制定探测策略
3. 执行多轮测试（replay_traffic/run_command）
4. 记录发现的漏洞（write_finding）
5. 沉淀可复用知识（write_corpus）
6. 完成后调用 done

### 可用工具
- 全套查询工具：list_traffic、view_traffic、replay_traffic
- 凭据管理：read_credentials、write_credential
- 发现管理：read_findings、write_finding、update_finding
- 知识库：search_corpus、write_corpus
- 情报黑板：write_lead
- 技能库：read_tooling_skill、read_vuln_skill
- 执行：run_command
- 结束：done
`

	return dispatcher.Profile{
		Complexity:   knowledgegraph.ComplexityModerate,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic", "replay_traffic",
			"read_credentials", "write_credential",
			"read_findings", "write_finding", "update_finding",
			"search_corpus", "write_corpus",
			"write_lead",
			"read_tooling_skill", "read_vuln_skill",
			"run_command",
			"done",
		},
		Budget: executor.Budget{
			MaxSteps:          40,
			MaxTokens:         80000,
			WatchdogSecs:      360,
			CompactionTrigger: 0.70,
		},
		Settle: executor.SettleConfig{
			Threshold:    0.85,
			Directive:    "预算接近上限，整理已验证的发现写入 finding，然后调用 done。",
			AllowedTools: []string{"write_finding", "update_finding", "write_lead", "done"},
		},
	}
}

// Complex 返回复杂任务的 Profile（~50 步）。
// 适用场景：漏洞利用、深度分析、多步骤攻击链。
func Complex(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务复杂度：复杂（complex）

预期执行步数：~50 步
适用场景：漏洞利用、深度分析、多步骤攻击链

### 工作流程
1. 深度分析目标和上下文
2. 制定详细利用策略
3. 执行多轮测试和调整
4. 使用浏览器自动化（browser_use）处理复杂交互
5. 完整记录利用过程和证据
6. 沉淀高价值知识
7. 完成后调用 done

### 可用工具
- 全套工具（包含 browser_use）
- 可执行复杂命令和脚本
- 可使用知识库加速分析
`

	return dispatcher.Profile{
		Complexity:   knowledgegraph.ComplexityComplex,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic", "replay_traffic",
			"read_credentials", "write_credential",
			"read_findings", "write_finding", "update_finding",
			"search_corpus", "write_corpus",
			"write_lead",
			"read_tooling_skill", "read_vuln_skill",
			"run_command", "browser_use",
			"done",
		},
		Budget: executor.Budget{
			MaxSteps:          60,
			MaxTokens:         120000,
			WatchdogSecs:      600,
			CompactionTrigger: 0.70,
		},
		Settle: executor.SettleConfig{
			Threshold:    0.85,
			Directive:    "预算接近上限，整理已验证的漏洞和利用链写入 finding，然后调用 done。",
			AllowedTools: []string{"write_finding", "update_finding", "write_credential", "write_lead", "done"},
		},
	}
}

// Extreme 返回极限任务的 Profile（~100 步）。
// 适用场景：权限提升、横向移动、复杂攻击场景、深度渗透。
func Extreme(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务复杂度：极限（extreme）

预期执行步数：~100 步
适用场景：权限提升、横向移动、复杂攻击场景、深度渗透

### 工作流程
1. 全面分析环境和上下文
2. 制定多阶段攻击策略
3. 执行长链路攻击（提权→横移→持久化）
4. 处理复杂的依赖关系和交互
5. 完整记录每个阶段的成果
6. 沉淀攻击链和技战术知识
7. 完成后调用 done

### 可用工具
- 全套工具无限制
- 可执行长时间运行的任务
- 可使用所有高级功能
`

	return dispatcher.Profile{
		Complexity:   knowledgegraph.ComplexityExtreme,
		SystemPrompt: systemPrompt + body,
		Tools:        nil, // nil = 全部工具可用
		Budget: executor.Budget{
			MaxSteps:          100,
			MaxTokens:         200000,
			WatchdogSecs:      900,
			CompactionTrigger: 0.70,
		},
		Settle: executor.SettleConfig{
			Threshold:    0.85,
			Directive:    "预算接近上限，整理完整攻击链和所有发现写入 finding，然后调用 done。",
			AllowedTools: []string{"write_finding", "update_finding", "write_credential", "write_lead", "write_corpus", "done"},
		},
	}
}
