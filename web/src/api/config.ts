/**
 * 配置管理 API 客户端（scenario / hunter 两资源 CRUD + 只读工具目录）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 删除单独实现：hunter 被 solo 场景引用时后端返回 409 + 中文 error，
 * 需把该提示透出给调用方（通用 del 只抛 HTTP 状态码，丢了中文原因）。
 */

import { get, post, put, getApiKey } from './client'
import type { ScenarioConfig, HunterConfig, ToolingTool } from './types'

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
    domain: sc.domain,
    engine: sc.engine,
    solo_hunter_id: sc.solo_hunter_id,
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

// ── hunter ────────────────────────────────────────────────────────────

/** 拉全量猎手（含 orchestrator/domain 两类、含 disabled）。 */
export async function listHunterConfigs(): Promise<HunterConfig[]> {
  return (await get<{ hunters: HunterConfig[] }>('/hunters')).hunters
}

/** 保存猎手：有 id 走 PUT，否则 POST。 */
export async function saveHunter(h: HunterConfig): Promise<HunterConfig> {
  const body = {
    code: h.code,
    kind: h.kind,
    name: h.name,
    description: h.description,
    body: h.body,
    tools: h.tools,
    cli_tools: h.cli_tools,
    max_iterations: h.max_iterations,
    enabled: h.enabled,
  }
  const res = h.id
    ? await put<{ hunter: HunterConfig }>(`/hunters/${h.id}`, body)
    : await post<{ hunter: HunterConfig }>('/hunters', body)
  return res.hunter
}

/** 删除猎手；被 solo 场景引用时后端 409 → 抛带中文原因的错。 */
export async function deleteHunter(id: string): Promise<void> {
  await delConfig(`/hunters/${id}`)
}

// ── tooling（只读）────────────────────────────────────────────────────

/** 拉外置 CLI 工具目录全集（HunterAdmin cli_tools 白名单多选器候选）。 */
export async function listToolingTools(): Promise<ToolingTool[]> {
  return (await get<{ tools: ToolingTool[] }>('/tooling/tools')).tools
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
