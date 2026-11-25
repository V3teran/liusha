/**
 * 配置 API 客户端：智能体、技能、场景与工具目录。
 *
 * PATCH 只提交改动的字段（智能体的名单字段为 SetChange），DELETE …/overrides[/:field] 恢复出厂。
 * 四个智能体以 code 寻址、不可增删；技能与场景的内置条目随版本更新（没改过的自动跟随新版），
 * 内置与自建都可编辑、删除——删除的内置条目升级不再补回，可在「已删除的内置」里恢复。
 */

import { get, post, put, patch, del } from './client'
import type {
  AgentDetail,
  AgentListItem,
  Scenario,
  ScenarioDetail,
  ScenarioListItem,
  SetChange,
  Skill,
  SkillDetail,
  SkillListItem,
  Tool,
  ToolKind,
  ToolDetail,
  ToolListResponse,
} from './types'

const enc = encodeURIComponent

// ── 智能体 ──────────────────────────────────────────────────────────

/** 拉全部智能体（不分页；模型配置页「能力路由」消费）。 */
export async function listAgents(): Promise<AgentListItem[]> {
  return (await get<{ agents: AgentListItem[] }>('/agents')).agents
}

/** 分页 + 搜索获取智能体（智能体页）。 */
export async function listAgentsPaged(
  page: number,
  size: number,
  search = '',
): Promise<{ agents: AgentListItem[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (search) params.set('search', search)
  return get<{ agents: AgentListItem[]; total: number }>(`/agents?${params}`)
}

/** 智能体详情：生效配置、出厂值、自定义状态、提示词归档与只读输出契约。 */
export async function getAgent(code: string): Promise<AgentDetail> {
  return get<AgentDetail>(`/agents/${enc(code)}`)
}

/**
 * 智能体的一次修改：只含改动的字段。整值字段提交新值（与出厂相同即不算自定义），
 * 名单字段提交 SetChange。名称、标识是固定身份，不可改。
 */
export interface AgentPatch {
  charter?: string
  description?: string
  max_iterations?: number
  complexity?: string
  function_tools?: SetChange
  cli_tools?: SetChange
  skills?: SetChange
}

export async function patchAgent(code: string, changes: AgentPatch): Promise<AgentDetail> {
  return patch<AgentDetail>(`/agents/${enc(code)}`, changes)
}

/** 把智能体的某字段（不传则全部字段）恢复为出厂值。 */
export async function resetAgent(code: string, field?: string): Promise<AgentDetail> {
  const suffix = field ? `/${enc(field)}` : ''
  return del<AgentDetail>(`/agents/${enc(code)}/overrides${suffix}`)
}

/** 删除升级前的提示词归档（用户已对照处理）。 */
export async function deletePromptArchive(code: string): Promise<void> {
  await del(`/agents/${enc(code)}/prompt-archive`)
}

// ── 技能 ────────────────────────────────────────────────────────────

/** 拉全部技能（智能体页技能多选器的候选）。 */
export async function listSkillCandidates(): Promise<SkillListItem[]> {
  return (await get<{ skills: SkillListItem[] }>('/skills')).skills
}

/** 分页 + 搜索获取技能（技能页）。 */
export async function listSkillsPaged(
  page: number,
  size: number,
  search = '',
): Promise<{ skills: SkillListItem[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (search) params.set('search', search)
  return get<{ skills: SkillListItem[]; total: number }>(`/skills?${params}`)
}

/** 技能详情：生效内容、出厂内容（内置）、自定义状态与各智能体的装配态。 */
export async function getSkill(code: string): Promise<SkillDetail> {
  return get<SkillDetail>(`/skills/${enc(code)}`)
}

/** 新建自建技能的字段（code 建后固定；与内置或已有技能同名时后端返回 409）。 */
export interface NewSkill {
  /** 缺省由服务端按名称生成（slug + 随机后缀）；显式传值用于导入等寻址名可控的场景。 */
  code?: string
  name: string
  description: string
  category: string
  body: string
}

export async function createSkill(payload: NewSkill): Promise<Skill> {
  return post<Skill>('/skills', payload)
}

/** 技能的一次修改：只含改动的字段（内置与自建同一路径）。 */
export interface SkillPatch {
  name?: string
  description?: string
  body?: string
  enabled?: boolean
}

export async function patchSkill(code: string, changes: SkillPatch): Promise<SkillDetail> {
  return patch<SkillDetail>(`/skills/${enc(code)}`, changes)
}

/** 把内置技能的某字段（不传则全部字段）恢复为出厂内容。 */
export async function resetSkill(code: string, field?: string): Promise<SkillDetail> {
  const suffix = field ? `/${enc(field)}` : ''
  return del<SkillDetail>(`/skills/${enc(code)}/overrides${suffix}`)
}

/** 删除技能（同时清除各智能体对它的引用）；内置技能可在「已删除的内置」里恢复。 */
export async function deleteSkill(code: string): Promise<void> {
  await del(`/skills/${enc(code)}`)
}

/** 被删除、可恢复的内置技能（内容为当前出厂）。 */
export async function listDeletedSkills(): Promise<Skill[]> {
  return (await get<{ skills: Skill[] }>('/skills?deleted=true')).skills
}

/** 把删除的内置技能恢复成当前出厂内容。 */
export async function restoreSkill(code: string): Promise<SkillDetail> {
  return post<SkillDetail>(`/skills/${enc(code)}/restore`)
}

// ── 场景 ────────────────────────────────────────────────────────────

/** 全部场景（下发入口的场景选择框用；内置在前）。 */
export async function listScenarios(): Promise<ScenarioListItem[]> {
  return (await get<{ scenarios: ScenarioListItem[] }>('/scenarios')).scenarios
}

/** 分页 + 搜索获取场景（场景页）。 */
export async function listScenariosPaged(
  page: number,
  size: number,
  search = '',
): Promise<{ scenarios: ScenarioListItem[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (search) params.set('search', search)
  return get<{ scenarios: ScenarioListItem[]; total: number }>(`/scenarios?${params}`)
}

export async function getScenario(code: string): Promise<ScenarioDetail> {
  return get<ScenarioDetail>(`/scenarios/${enc(code)}`)
}

/** 新建自建场景（与内置或已有场景同名时后端返回 409）。 */
export interface NewScenario {
  /** 缺省由服务端按名称生成（slug + 随机后缀）；显式传值用于导入等寻址名可控的场景。 */
  code?: string
  name: string
  description: string
  body: string
}

export async function createScenario(payload: NewScenario): Promise<Scenario> {
  return post<Scenario>('/scenarios', payload)
}

/** 场景的一次修改：只含改动的字段。 */
export interface ScenarioPatch {
  name?: string
  description?: string
  body?: string
  enabled?: boolean
}

export async function patchScenario(code: string, changes: ScenarioPatch): Promise<ScenarioDetail> {
  return patch<ScenarioDetail>(`/scenarios/${enc(code)}`, changes)
}

/** 把内置场景的某字段（不传则全部字段）恢复为出厂内容。 */
export async function resetScenario(code: string, field?: string): Promise<ScenarioDetail> {
  const suffix = field ? `/${enc(field)}` : ''
  return del<ScenarioDetail>(`/scenarios/${enc(code)}/overrides${suffix}`)
}

/** 删除场景（被定时模板引用时返回 409；已建任务不受影响）；内置场景可在「已删除的内置」里恢复。 */
export async function deleteScenario(code: string): Promise<void> {
  await del(`/scenarios/${enc(code)}`)
}

/** 被删除、可恢复的内置场景（内容为当前出厂）。 */
export async function listDeletedScenarios(): Promise<Scenario[]> {
  return (await get<{ scenarios: Scenario[] }>('/scenarios?deleted=true')).scenarios
}

/** 把删除的内置场景恢复成当前出厂内容。 */
export async function restoreScenario(code: string): Promise<ScenarioDetail> {
  return post<ScenarioDetail>(`/scenarios/${enc(code)}/restore`)
}

/** 从技能侧装配/卸载某智能体的该技能（记为该智能体技能名单的勾选或取消）。 */
export async function assignSkill(code: string, agentCode: string, involved: boolean): Promise<void> {
  await put(`/skills/${enc(code)}/agents/${enc(agentCode)}`, { involved })
}

// ── 工具目录（只读）────────────────────────────────────────────────────

// listTools 的查询参数：kind 过滤、search 关键词、分页（page/size 缺省 = 全量不分页）。
export interface ToolQuery {
  kind?: ToolKind
  search?: string
  page?: number // 1-based；缺省 = 全量（智能体多选器候选用全量）
  size?: number
}

/**
 * 拉工具目录。
 *   - 传 page → 分页（响应带 total），工具模块列表用。
 *   - 不传 page → 全量（total = 列表长度），智能体页多选器候选用。
 */
export async function listTools(query: ToolQuery = {}): Promise<ToolListResponse> {
  const qs = new URLSearchParams()
  if (query.kind) qs.set('kind', query.kind)
  if (query.search) qs.set('search', query.search)
  if (query.page) qs.set('page', String(query.page))
  if (query.size) qs.set('size', String(query.size))
  const suffix = qs.toString() ? `?${qs}` : ''
  return get<ToolListResponse>(`/tools${suffix}`)
}

/** 拉某类工具全量候选（不分页），供智能体页 function_tools/cli_tools 多选器。 */
export async function listToolCandidates(kind: ToolKind): Promise<Tool[]> {
  return (await listTools({ kind })).tools
}

/** 拉工具详情（含全部智能体及各自装配态 involved），工具模块详情面板用。 */
export async function getTool(name: string): Promise<ToolDetail> {
  return get<ToolDetail>(`/tools/${enc(name)}`)
}

/** 从工具侧装配/卸载某智能体的该工具（记为该智能体对应名单的勾选或取消）。 */
export async function assignTool(name: string, code: string, involved: boolean): Promise<void> {
  await put<{ code: string; involved: boolean }>(`/tools/${enc(name)}/agents/${enc(code)}`, { involved })
}
