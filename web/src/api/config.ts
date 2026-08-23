/**
 * 配置管理 API 客户端（scenario / executor 两资源 CRUD + 只读工具目录）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 删除单独实现：executor 被 solo 场景引用时后端返回 409 + 中文 error，
 * 需把该提示透出给调用方（通用 del 只抛 HTTP 状态码，丢了中文原因）。
 */

import { get, post, put, patch, getApiKey } from './client'
import type {
  ScenarioConfig,
  AgentConfig,
  Tool,
  ToolKind,
  ToolDetail,
  ToolListResponse,
} from './types'

// ── scenario ──────────────────────────────────────────────────────────
// 读取（列表/单条）走 client.ts 的 listScenarios（GET /scenarios 单一口径，全量全字段）。
// 此处仅保留变更操作（保存/删除）。

/** 保存场景：有 id 走 PUT（按 id），否则 POST（新建，upsert-by-code）。 */
export async function saveScenario(sc: ScenarioConfig): Promise<ScenarioConfig> {
  const body = {
    code: sc.code,
    name: sc.name,
    description: sc.description,
    instruction: sc.instruction,
    engine: sc.engine,
    solo_agent_id: sc.solo_agent_id,
    enabled: sc.enabled,
  }
  const res = sc.id
    ? await put<{ scenario: ScenarioConfig }>(`/scenarios/${sc.id}`, body)
    : await post<{ scenario: ScenarioConfig }>('/scenarios', body)
  return res.scenario
}

/** 删除场景（task.scenario_id 无 FK，不会撞 RESTRICT）。 */
export async function deleteScenario(id: string): Promise<void> {
  await delConfig(`/scenarios/${id}`)
}

// ── executor ──────────────────────────────────────────────────────────

/** 拉全量操作员（含 planner/executor 两类、含 disabled）。 */
export async function listAgentConfigs(): Promise<AgentConfig[]> {
  return (await get<{ agents: AgentConfig[] }>('/executors')).agents
}

/** 保存操作员：有 id 走 PUT，否则 POST。 */
export async function saveAgent(h: AgentConfig): Promise<AgentConfig> {
  const body = {
    code: h.code,
    kind: h.kind,
    name: h.name,
    description: h.description,
    body: h.body,
    function_tools: h.function_tools,
    cli_tools: h.cli_tools,
    max_iterations: h.max_iterations,
    tier: h.tier,
    enabled: h.enabled,
  }
  const res = h.id
    ? await put<{ agent: AgentConfig }>(`/executors/${h.id}`, body)
    : await post<{ agent: AgentConfig }>('/executors', body)
  return res.agent
}

/**
 * 改操作员能力档（分档页「每档选 agent」移档用）：只 PATCH tier 单字段，
 * 不整体 upsert——避免用列表快照覆盖别处刚改的 body/工具。返回回读的完整 executor。
 */
export async function saveAgentTier(id: string, tier: string): Promise<AgentConfig> {
  const res = await patch<{ agent: AgentConfig }>(`/executors/${id}/tier`, { tier })
  return res.agent
}

/** 删除操作员；被 solo 场景引用时后端 409 → 抛带中文原因的错。 */
export async function deleteAgent(id: string): Promise<void> {
  await delConfig(`/executors/${id}`)
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
 *   - 不传 page → 全量（total = 列表长度），AgentAdmin 多选器候选用。
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

/** 拉某工具全量候选（不分页），供 AgentAdmin function_tools/cli_tools 多选器。 */
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

/**
 * 配置删除专用 DELETE：409（被下游引用，FK RESTRICT）时读后端中文 error 抛出，
 * 供调用方 alert 提示「先解除引用再删」。其他非 2xx 抛通用状态码错。
 */
async function delConfig(path: string): Promise<void> {
  const res = await fetch('/api' + path, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (res.ok) return
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error || '该配置仍被引用，无法删除')
  }
  throw new Error(`DELETE ${path} → ${res.status}`)
}
