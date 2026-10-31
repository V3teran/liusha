/**
 * 凭证库域 API 客户端（GET /credentials、GET/PUT/DELETE /credentials/:host…）
 *
 * 凭证值全量明文（操作台要看/要复制原文）；无任何 TTL 语义——
 * 生命周期由任务终态清理/清扫器/人工删除驱动。
 * 复用 client.ts 的 fetch 封装（单一 X-API-Key 鉴权口径）。
 */

import { del, get, put } from './client'
import type { CredentialEntry, CredentialHostSummary, CredentialIdentity } from './types'

/** host 汇总枚举（只含身份计数，不含任何凭证值）。 */
export async function listCredentialHosts(): Promise<CredentialHostSummary[]> {
  return (await get<{ hosts: CredentialHostSummary[] }>('/credentials')).hosts
}

/** 单 host 身份详情（全量明文，含会话任务状态注入）。 */
export async function getCredentialDetail(
  host: string,
): Promise<{ host: string; identities: CredentialIdentity[] }> {
  return get(`/credentials/${encodeURIComponent(host)}`)
}

/** upsert 单个登录身份（host 经服务端 CanonicalHost 归一）。 */
export async function saveCredentialIdentity(
  host: string,
  identity: { username: string; role?: string; credentials: CredentialEntry[] },
): Promise<void> {
  await put(`/credentials/${encodeURIComponent(host)}/usernames`, identity)
}

/** 删除单个登录身份。 */
export async function deleteCredentialIdentity(host: string, username: string): Promise<void> {
  await del(`/credentials/${encodeURIComponent(host)}/usernames/${encodeURIComponent(username)}`)
}

/** 清空该 host 会话身份（持久身份不动），返回删除数。 */
export async function purgeCredentialSessions(host: string): Promise<number> {
  const r = await del<{ deleted: number }>(`/credentials/${encodeURIComponent(host)}/sessions`)
  return r.deleted
}

/** 清空该 host 全部身份。 */
export async function deleteCredentialHost(host: string): Promise<void> {
  await del(`/credentials/${encodeURIComponent(host)}`)
}
