/**
 * 附件域 API 客户端（POST /attachments、POST /conversations/:id/attachments）。
 *
 * multipart 上传（同源 Cookie 自动携带）：会话已存在走 :id 形态，新会话尚未
 * 创建走 /attachments（服务端落 pending 暂存区，随首条消息认领）。
 * 错误响应沿用全站中文错误体口径（{"error": "..."}），就地抛给 Composer 展示。
 */

import { API_BASE } from './client'
import type { AttachmentRef } from './types'

export async function uploadAttachment(file: File, conversationID?: string): Promise<AttachmentRef> {
  const form = new FormData()
  form.append('file', file)
  if (conversationID) form.append('conversation_id', conversationID)
  const path = conversationID
    ? `/conversations/${encodeURIComponent(conversationID)}/attachments`
    : '/attachments'
  const res = await fetch(API_BASE + path, { method: 'POST', body: form })
  if (!res.ok) {
    let detail = ''
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) detail = `: ${body.error}`
    } catch {
      /* 非 JSON 错误体回退状态码 */
    }
    throw new Error(`上传失败 ${res.status}${detail}`)
  }
  const r = (await res.json()) as { name: string; size: number; path: string }
  return { name: r.name, size: r.size, path: r.path }
}

/** 附件下载 URL（工作区 raw 通道；时间线芯片与预览共用）。 */
export function attachmentDownloadURL(path: string): string {
  return `/api/workspaces/raw?path=${encodeURIComponent(path)}`
}
