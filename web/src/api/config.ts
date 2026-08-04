/**
 * 配置管理 API 客户端（scenario / playbook / hunter 三资源 CRUD）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 删除单独实现：playbook/hunter 被下游引用时后端返回 409 + 中文 error，
 * 需把该提示透出给调用方（通用 del 只抛 HTTP 状态码，丢了中文原因）。
 */

import { get, post, put, getApiKey } from './client'
import type { ScenarioConfig, PlaybookConfig, HunterConfig } from './types'

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
    playbook_id: sc.playbook_id,
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

// ── playbook ────────────────────────────────────────────────────────────

/** 拉全量剧本（列表页，不含 hunters 组合）。 */
export async function listPlaybookConfigs(): Promise<PlaybookConfig[]> {
  return (await get<{ playbooks: PlaybookConfig[] }>('/playbooks')).playbooks
}

/** 拉单个剧本（含有序 hunters 组合），编辑前取。 */
export async function getPlaybookConfig(id: string): Promise<PlaybookConfig> {
  return (await get<{ playbook: PlaybookConfig }>(`/playbooks/${id}`)).playbook
}

/**
 * 保存剧本：主体 upsert + 按序重设 hunters 组合（position 由数组下标决定）。
 * @param hunterIDs 有序领域猎手 id 列表
 */
export async function savePlaybook(pb: PlaybookConfig, hunterIDs: string[]): Promise<PlaybookConfig> {
  const body = {
    code: pb.code,
    name: pb.name,
    description: pb.description,
    enabled: pb.enabled,
    hunters: hunterIDs,
  }
  const res = pb.id
    ? await put<{ playbook: PlaybookConfig }>(`/playbooks/${pb.id}`, body)
    : await post<{ playbook: PlaybookConfig }>('/playbooks', body)
  return res.playbook
}

/** 删除剧本；被场景引用时后端 409 → 抛带中文原因的错。 */
export async function deletePlaybook(id: string): Promise<void> {
  await delConfig(`/playbooks/${id}`)
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
    max_iterations: h.max_iterations,
    enabled: h.enabled,
  }
  const res = h.id
    ? await put<{ hunter: HunterConfig }>(`/hunters/${h.id}`, body)
    : await post<{ hunter: HunterConfig }>('/hunters', body)
  return res.hunter
}

/** 删除猎手；被剧本引用时后端 409 → 抛带中文原因的错。 */
export async function deleteHunter(id: string): Promise<void> {
  await delConfig(`/hunters/${id}`)
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
