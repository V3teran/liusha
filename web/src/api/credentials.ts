/**
 * 凭证库域 API 客户端（GET /credentials、PUT/DELETE /credentials/:host/usernames…）
 *
 * 凭证值全量明文（操作台要看/要复制原文）；无任何 TTL 语义——
 * 生命周期由任务终态清理/清扫器/人工删除驱动。
 * 复用 client.ts 的 fetch 封装（同源携带会话 Cookie）。
 */

import { del, get, put } from './client'
import type { CredentialEntry, CredentialListItem } from './types'

/** 登录身份分页列表（跨 host；关键词检索、分页、总数都在服务端，按录入时间倒序）。 */
export function listCredentials(
  search: string,
  page: number,
  size: number,
): Promise<{ items: CredentialListItem[]; total: number }> {
  const p = new URLSearchParams({ page: String(page), size: String(size) })
  if (search) p.set('search', search)
  return get(`/credentials?${p}`)
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
