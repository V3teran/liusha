/**
 * 会话域 API 客户端（会话列表 / 消息回看 / 多轮续接）
 *
 * 复用 client.ts 的 fetch 封装（同源自动携带登录会话 Cookie）。
 * 特例语义（cookie / 409 哨兵）在各自函数上就地注明。
 */

import { API_BASE, get, patch, post } from './client'
import type { AttachmentRef, Conversation, Message } from './types'

/** 单个会话（标题、关联任务等）；不存在时抛 404。 */
export async function getConversation(convID: string): Promise<Conversation> {
  return get<Conversation>(`/conversations/${encodeURIComponent(convID)}`)
}

/**
 * 分页获取会话列表（offset 翻页——会话列表按 updated_at 排序，活跃会话会被顶到最前，
 * 没有稳定单调游标可用，故用 offset；这个数据量级下足够，翻页时小幅重排是可接受的权衡）。
 *
 * 来源与关键词都在服务端过滤（非前端在单页结果上再过滤）：分页边界必须建立在已过滤的集合上，
 * 否则其它页的会话搜不到，「共 N 个会话」与实际翻得到的条数也对不上。
 */
export interface ConversationQuery {
  limit?: number // 每页条数（后端默认 30）
  offset?: number // 跳过条数（默认 0 = 首页）
  source?: string // 下发来源（direct 人工下发 / proxy 代理捕获；纯聊天归 direct）
  search?: string // 关键词：会话标题、所属任务的目标或会话 ID 前缀
}

export async function listConversations({
  limit = 30,
  offset = 0,
  source = '',
  search = '',
}: ConversationQuery = {}): Promise<{ conversations: Conversation[]; hasMore: boolean; total: number }> {
  const q = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (source) q.set('source', source)
  if (search.trim()) q.set('search', search.trim())
  const res = await get<{ conversations: Conversation[] | null; has_more: boolean; total: number }>(`/conversations?${q}`)
  // 后端无数据时 conversations 返回 null（Go 的 nil slice 序列化为 null 而非 []）——
  // 归一为空数组，避免下游 items.length 等消费点炸。
  return { conversations: res.conversations ?? [], hasMore: res.has_more, total: res.total ?? 0 }
}

/**
 * 获取会话中的消息（自动分页拉全）。
 *
 * 后端单次返回上限 500 条（clampLimit），长会话（active 扫描动辄上千条事件）一次拉不完。
 * 故内部循环按 after_seq 翻页直到拉空——否则打开/刷新长会话只显示前 500 条，
 * 停在中途某条（实测停在 planner 收尾报告之前，用户看不到最终结果）。
 *
 * @param convID 会话 ID
 * @param afterSeq 起始游标，仅返回 Seq > afterSeq 的消息（默认 0 = 从头拉全）
 */
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  const PAGE = 500 // 与后端 maxListLimit 对齐：返回 < PAGE 即最后一页
  const all: Message[] = []
  let cursor = afterSeq
  for (;;) {
    // 后端零消息时返回 null（Go nil slice）——归一为空数组，防下游 for-of 在 null 上炸
    // （与 listConversations 的 null 归一同源防御；后端亦已归一，此处兜底旧响应）。
    const page = (
      await get<{ messages: Message[] | null }>(`/conversations/${convID}/messages?after_seq=${cursor}`)
    ).messages ?? []
    if (page.length === 0) break
    all.push(...page)
    cursor = page[page.length - 1].seq
    if (page.length < PAGE) break
  }
  return all
}

/**
 * 从会话发起任务
 * @param brief 任务目标描述
 * @returns conversation_id 和 task_id（闲聊/qa 意图不下发 task 时 task_id 为空）
 */
export async function startChat(
  brief: string,
  attachments: AttachmentRef[] = []
): Promise<{ conversation_id: string; task_id: string }> {
  return post('/chat', { brief, attachments: attachments.length > 0 ? attachments : undefined })
}

/**
 * 多轮：往已有会话追加动作消息。落库即 200，意图分类与分流在服务端异步完成——
 * 扫描进行中/暂停中收到 action 指令的反馈由时间线提示消息给出（无同步 busy 判定）。
 * @param convID 会话 ID
 * @param content 消息内容
 * @returns intent
 */
export async function followUp(
  convID: string,
  content: string,
  attachments: AttachmentRef[] = []
): Promise<{ intent: string }> {
  const res = await fetch(`${API_BASE}/conversations/${convID}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content, attachments: attachments.length > 0 ? attachments : undefined }),
  })
  if (!res.ok) throw new Error(`POST /conversations/${convID}/messages → ${res.status}`)
  return res.json()
}

// 删除会话及其消息（后端 message FK CASCADE 连带删；不动关联 scan/finding 成果）。
// 关联任务仍在跑时后端返回 409 → 抛 'TASK_ACTIVE' 哨兵，调用方提示「先停后删」。
export async function deleteConversation(convID: string): Promise<void> {
  const res = await fetch(`${API_BASE}/conversations/${convID}`, {
    method: 'DELETE',
  })
  if (res.status === 409) throw new Error('TASK_ACTIVE')
  if (!res.ok) throw new Error(`DELETE /conversations/${convID} → ${res.status}`)
}

// 重命名会话标题（PATCH /conversations/:id）。空 title → 后端存 NULL，展示回落首条消息摘要。
export async function renameConversation(convID: string, title: string): Promise<void> {
  await patch(`/conversations/${convID}`, { title })
}
