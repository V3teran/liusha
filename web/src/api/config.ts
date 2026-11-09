/**
 * 配置 API 客户端：智能体、技能与工具目录。
 *
 * 智能体与内置技能的出厂值随程序发布，接口只读写用户自定义：PATCH 只提交改动的字段
 * （名单字段为 SetChange），DELETE …/overrides[/:field] 恢复出厂。四个智能体以 code 寻址、
 * 不可增删；自建技能可增删改。
 */

import { get, post, put, patch, del } from './client'
import type {
  AgentDetail,
  AgentListItem,
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

/** 拉全部智能体（不分页；模型分档页消费）。 */
export async function listAgentConfigs(): Promise<AgentListItem[]> {
  return (await get<{ agents: AgentListItem[] }>('/agents')).agents
}

/** 分页 + 搜索获取智能体（智能体页）。 */
export async function listAgentsPaged(
  page: number,
  size: number,
  q = '',
): Promise<{ agents: AgentListItem[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (q) params.set('q', q)
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
  q = '',
): Promise<{ skills: SkillListItem[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (q) params.set('q', q)
  return get<{ skills: SkillListItem[]; total: number }>(`/skills?${params}`)
}

/** 技能详情：生效内容、出厂内容（内置）、自定义状态与各智能体的装配态。 */
export async function getSkill(code: string): Promise<SkillDetail> {
  return get<SkillDetail>(`/skills/${enc(code)}`)
}

/** 新建自建技能的字段（code 建后固定；与内置或已有技能同名时后端返回 409）。 */
export interface NewSkill {
  code: string
  name: string
  description: string
  category: string
  body: string
}

export async function createSkill(payload: NewSkill): Promise<Skill> {
  return post<Skill>('/skills', payload)
}

/** 技能的一次修改：只含改动的字段（内置技能写入自定义层，自建技能直接修改）。 */
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

/** 删除自建技能（同时清除各智能体对它的引用；内置技能返回 409）。 */
export async function deleteSkill(code: string): Promise<void> {
  await del(`/skills/${enc(code)}`)
}

/** 从技能侧装配/卸载某智能体的该技能（记为该智能体技能名单的勾选或取消）。 */
export async function assignSkill(code: string, agentCode: string, involved: boolean): Promise<void> {
  await put(`/skills/${enc(code)}/agents/${enc(agentCode)}`, { involved })
}

// ── 工具目录（只读）────────────────────────────────────────────────────

// listTools 的查询参数：kind 过滤、q 关键词、分页（page/size 缺省 = 全量不分页）。
export interface ToolQuery {
  kind?: ToolKind
  q?: string
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
  if (query.q) qs.set('q', query.q)
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
