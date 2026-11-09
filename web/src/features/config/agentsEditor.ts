// 智能体配置编辑的纯逻辑：详情 → 编辑草稿 → 只含改动字段的 PATCH 载荷。
// 独立于渲染组件，便于单测。

import type { AgentPatch } from '@/api/config'
import type { AgentDetail } from '@/api/types'
import { setChangeOf, setDraftOf, type SetDraft } from '@/lib/override'

/** 名单字段及其全集（展开「全部」与识别失效项用）。 */
export type AgentSetField = 'function_tools' | 'cli_tools' | 'skills'

export type Catalogs = Record<AgentSetField, string[]>

export const SET_FIELDS: AgentSetField[] = ['function_tools', 'cli_tools', 'skills']

/** 智能体编辑草稿：整值字段为值，名单字段为编辑态（全部模式 + 勾选项）。 */
export interface AgentDraft {
  charter: string
  description: string
  max_iterations: number
  complexity: string
  function_tools: SetDraft
  cli_tools: SetDraft
  skills: SetDraft
}

export function draftOf(detail: AgentDetail, catalogs: Catalogs): AgentDraft {
  const { agent, defaults, overrides } = detail
  const setDraft = (f: AgentSetField) => setDraftOf(agent[f], defaults[f], overrides[f], catalogs[f])
  return {
    charter: agent.charter,
    description: agent.description,
    max_iterations: agent.max_iterations,
    complexity: agent.complexity,
    function_tools: setDraft('function_tools'),
    cli_tools: setDraft('cli_tools'),
    skills: setDraft('skills'),
  }
}

/** 草稿相对基线的改动 → PATCH 载荷（空对象 = 无改动）。 */
export function patchOf(baseline: AgentDraft, draft: AgentDraft, catalogs: Catalogs): AgentPatch {
  const p: AgentPatch = {}
  if (draft.charter !== baseline.charter) p.charter = draft.charter
  if (draft.description !== baseline.description) p.description = draft.description
  if (draft.max_iterations !== baseline.max_iterations) p.max_iterations = draft.max_iterations
  if (draft.complexity !== baseline.complexity) p.complexity = draft.complexity
  for (const f of SET_FIELDS) {
    const change = setChangeOf(baseline[f], draft[f], catalogs[f])
    if (change) p[f] = change
  }
  return p
}

/**
 * 恢复某字段后的新草稿：以服务端新基线为底，保留用户在其它字段上尚未保存的改动
 * （恢复一项不应吞掉别处的编辑）。
 */
export function rebaseDraft(
  oldBaseline: AgentDraft,
  draft: AgentDraft,
  newBaseline: AgentDraft,
  resetField?: keyof AgentDraft,
): AgentDraft {
  const next = { ...newBaseline }
  for (const key of Object.keys(draft) as Array<keyof AgentDraft>) {
    if (key === resetField) continue
    if (JSON.stringify(draft[key]) !== JSON.stringify(oldBaseline[key])) {
      Object.assign(next, { [key]: draft[key] })
    }
  }
  return next
}
