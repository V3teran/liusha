/**
 * 配置管理 API 客户端（agent 配置读取/编辑 + 只读工具目录）
 *
 * 复用 client.ts 的 get/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 角色为固定种子（四角色以 code 寻址）：只有编辑、没有新建/删除。
 */

import { get, post, put, patch, del } from './client'
import type {
  AgentConfig,
  Skill,
  Tool,
  ToolKind,
  ToolDetail,
  ToolListResponse,
} from './types'

// ── agent 配置 ────────────────────────────────────────────────────────

/** 拉全量 agent 配置（四角色、含 disabled；分档页消费面）。 */
export async function listAgentConfigs(): Promise<AgentConfig[]> {
  const res = await get<{ agents: AgentConfig[] }>('/agents')
  return res.agents
}

/**
 * 智能体可变字段集合：后端 agentUpdate/UpdateParams 白名单。
 * 名称/标识符是固定种子身份，不在其中；description（调度摘要）是 LLM 可读面、可编辑——
 * 接口层直接收窄，防止调用方把改不了的字段发上去。
 */
export interface AgentChanges {
  system_prompt?: string
  description?: string
  function_tools?: string[]
  cli_tools?: string[]
  skills?: string[]
  max_iterations?: number
  complexity?: string
}

/**
 * 保存智能体：PUT 部分更新——只提交 changes 里出现的字段，未提及字段后端不动，
 * 与工具页「单工具装配」（PUT /tools/:name/agents/:code）并发写互不覆盖快照。
 * 目标行由 URL id 权威定位（与 GET 同律），body 即纯变更集，无寻址字段。
 */
export async function saveAgent(id: string, changes: AgentChanges): Promise<void> {
  await put(`/agents/${encodeURIComponent(id)}`, changes)
}

/**
 * 改操作员复杂度档（分档页移档用）：只 PATCH complexity 单字段，
 * 不整体 upsert——避免用列表快照覆盖别处刚改的配置。返回回读的完整 agent。
 */
export async function saveAgentComplexity(id: string, complexity: string): Promise<AgentConfig> {
  const res = await patch<{ agent: AgentConfig }>(`/agents/${id}/complexity`, { complexity })
  return res.agent
}

/** 拉 skill 目录全量（GET /skills），供 AgentsPage skills 多选器候选。 */
export async function listSkillCandidates(): Promise<Skill[]> {
  return get<Skill[]>('/skills')
}

// ── skill CRUD（技能页）──────────────────────────────────────────────

/** 新建/编辑技能的可写字段。code 建后固定；is_builtin 由后端定（新建恒 false）。 */
export interface SkillPayload {
  code: string
  name: string
  description: string
  category: string
  body: string
  enabled: boolean
}

/** 编辑已有技能的可变字段（PUT 部分更新；内置技能仅 enabled 有意义）。 */
export interface SkillChanges {
  name?: string
  description?: string
  category?: string
  body?: string
  enabled?: boolean
}

/** 新建自建技能（is_builtin=false，不受 reseed 覆盖）。code 冲突由后端报错。 */
export async function createSkill(payload: SkillPayload): Promise<Skill> {
  return post<Skill>('/skills', payload)
}

/** 更新技能（按 code 寻址，部分更新——未提及字段不动）。 */
export async function updateSkill(code: string, changes: SkillChanges): Promise<Skill> {
  return put<Skill>(`/skills/${encodeURIComponent(code)}`, changes)
}

/** 删除技能（后端拒绝内置技能：409）。 */
export async function deleteSkill(code: string): Promise<void> {
  await del(`/skills/${encodeURIComponent(code)}`)
}

// ── tool（只读目录）────────────────────────────────────────────────────

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
 *   - 不传 page → 全量（total = 列表长度），AgentsPage 多选器候选用。
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

/** 拉某工具全量候选（不分页），供 AgentsPage function_tools/cli_tools 多选器。 */
export async function listToolCandidates(kind: ToolKind): Promise<Tool[]> {
  return (await listTools({ kind })).tools
}

/** 拉工具详情（含全量智能体及各自装配态 involved），工具模块详情面板用。 */
export async function getTool(name: string): Promise<ToolDetail> {
  return get<ToolDetail>(`/tools/${encodeURIComponent(name)}`)
}

/**
 * 从工具侧装配（involved=true）/卸载（false）某智能体的该工具。
 * 后端按工具 kind 改该智能体的 function_tools / cli_tools 并回存，幂等。
 * 装配读写都在后端（单一权威），前端不再搬运智能体全量 body。
 */
export async function assignTool(
  name: string,
  code: string,
  involved: boolean,
): Promise<void> {
  await put<{ code: string; involved: boolean }>(
    `/tools/${encodeURIComponent(name)}/agents/${encodeURIComponent(code)}`,
    { involved },
  )
}
