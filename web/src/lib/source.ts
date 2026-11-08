// 下发来源（assignment.source）→ 展示文案 / 色。单一真相源：任务表、漏洞台账、漏洞抽屉、
// 各筛选下拉全部从此取值，保证同一概念全站一个叫法。
// 取值与 DB assignment_source_check 一致：direct 自主下发 / proxy 代理模式。

export type Source = 'direct' | 'proxy'

const SOURCE_META: Record<Source, { label: string; color: string }> = {
  direct: { label: '自主下发', color: 'var(--source-direct)' },
  proxy: { label: '代理模式', color: 'var(--source-proxy)' },
}

/** 来源文案；未知值原样返回（不臆造）。 */
export function sourceLabel(source: string): string {
  return SOURCE_META[source as Source]?.label ?? source
}

/** 来源色（未知值落中性灰）。 */
export function sourceColor(source: string): string {
  return SOURCE_META[source as Source]?.color ?? 'var(--status-neutral)'
}

/** 筛选下拉选项（固定顺序：自主下发在前）。 */
export const SOURCE_OPTIONS: ReadonlyArray<{ value: Source; label: string }> = [
  { value: 'direct', label: SOURCE_META.direct.label },
  { value: 'proxy', label: SOURCE_META.proxy.label },
]

/**
 * 会话页路由：对话按来源分两个 tab，路径段即来源值（/conversations/direct | proxy）。
 * 缺失或未知来源落自主下发 tab——纯聊天与无 task 的会话归自主侧（与后端列表分流一致）。
 */
export function conversationPath(source: string | undefined, conversationId?: string): string {
  const tab: Source = source === 'proxy' ? 'proxy' : 'direct'
  return conversationId ? `/conversations/${tab}?conv=${encodeURIComponent(conversationId)}` : `/conversations/${tab}`
}
