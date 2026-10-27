// 智能体配置编辑的纯逻辑：全量哨兵（["*"]）编辑语义 + 脏字段 diff。
// 独立于渲染组件，便于单测。

import type { AgentChanges } from '@/api/config'
import type { AgentConfig } from '@/api/types'

// 全量哨兵判定：数组恰为 ["*"] 时运行时语义 = 自动装配该类全部（含未来新增）。
export function isWildcard(list: string[]): boolean {
  return list.length === 1 && list[0] === '*'
}

// picker 的选中视图：哨兵态展示为目录全集全勾选——与显式白名单形式完全一致；
// 取消勾选由 toggleTool 物化为「全集减一」。
export function wildcardView(list: string[], catalogNames: string[]): string[] {
  return isWildcard(list) ? catalogNames : list
}

const sameList = (a: string[], b: string[]) =>
  a.length === b.length && a.every((v, i) => v === b[i])

// baseline → draft 的字段级差异（仅可变字段）。保存只提交这些脏 diff，
// 绝不回传整行快照——否则工具页并发装配的工具集会被旧快照覆盖回去。
export function diffChanges(baseline: AgentConfig, draft: AgentConfig): AgentChanges {
  const c: AgentChanges = {}
  if (draft.system_prompt !== baseline.system_prompt) c.system_prompt = draft.system_prompt
  if (draft.description !== baseline.description) c.description = draft.description
  if (draft.complexity !== baseline.complexity) c.complexity = draft.complexity
  if (draft.max_iterations !== baseline.max_iterations) c.max_iterations = draft.max_iterations
  if (!sameList(draft.function_tools, baseline.function_tools)) c.function_tools = draft.function_tools
  if (!sameList(draft.cli_tools, baseline.cli_tools)) c.cli_tools = draft.cli_tools
  if (!sameList(draft.skills, baseline.skills)) c.skills = draft.skills
  return c
}
