/**
 * API 客户端
 *
 * 与 liusha Go 后端通信的 fetch 封装。
 * API key 存储于 sessionStorage，所有非 SSE 请求都带 X-API-Key header。
 */

import type { Conversation, Message, Role } from './types'

const KEY_STORAGE = 'liusha_api_key'

/**
 * 设置 API key（存于 sessionStorage）
 */
export function setApiKey(k: string) {
  sessionStorage.setItem(KEY_STORAGE, k)
}

/**
 * 获取 API key；未设置时返回空字符串
 */
export function getApiKey(): string {
  return sessionStorage.getItem(KEY_STORAGE) ?? ''
}

/**
 * 发起 GET 请求，自动添加 X-API-Key header
 */
async function get<T>(path: string): Promise<T> {
  const res = await fetch('/api' + path, {
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`GET ${path} → ${res.status}`)
  return res.json()
}

/**
 * 获取扫描角色列表
 */
export async function listRoles(): Promise<Role[]> {
  return (await get<{ roles: Role[] }>('/roles')).roles
}

/**
 * 获取对话列表
 */
export async function listConversations(): Promise<Conversation[]> {
  return (await get<{ conversations: Conversation[] }>('/conversations')).conversations
}

/**
 * 获取对话中的消息
 * @param convID 对话 ID
 * @param afterSeq 仅返回 Seq > afterSeq 的消息（默认 0 = 全部）
 */
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  return (await get<{ messages: Message[] }>(`/conversations/${convID}/messages?after_seq=${afterSeq}`))
    .messages
}

/**
 * 发起对话扫描
 * 成功后后端 Set-Cookie liusha_stream（SSE 鉴权用）
 * @param brief 扫描目标描述
 * @param roleID 角色 ID
 * @returns conversation_id 和 scan_id
 */
export async function startChat(
  brief: string,
  roleID: string
): Promise<{ conversation_id: string; scan_id: string }> {
  const res = await fetch('/api/chat', {
    method: 'POST',
    headers: {
      'X-API-Key': getApiKey(),
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ brief, role_id: roleID }),
  })
  if (!res.ok) throw new Error(`POST /chat → ${res.status}`)
  return res.json()
}
