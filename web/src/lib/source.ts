// 下发来源（assignment.source）→ 展示文案 / 色。单一真相源：任务表、漏洞台账、漏洞抽屉、
// 各筛选下拉全部从此取值，保证同一概念全站一个叫法。
// 取值与 DB assignment_source_check 一致：direct 人工下发 / proxy 代理捕获。

export type Source = 'direct' | 'proxy'

const SOURCE_META: Record<Source, { label: string; color: string }> = {
  direct: { label: '人工下发', color: 'var(--source-direct)' },
  proxy: { label: '代理捕获', color: 'var(--source-proxy)' },
}

/** 来源文案；未知值原样返回（不臆造）。 */
export function sourceLabel(source: string): string {
  return SOURCE_META[source as Source]?.label ?? source
}

/** 来源色（未知值落中性灰）。 */
export function sourceColor(source: string): string {
  return SOURCE_META[source as Source]?.color ?? 'var(--status-neutral)'
}

/** 代理捕获的接入说明：代理批次与代理流量两处空态共用。 */
export const PROXY_SETUP_HINT =
  '把浏览器的 HTTP 代理设为 liusha 代理入口（二进制部署默认 8588 端口，容器部署默认 58588）后浏览目标站，流量会自动分批交给智能体分析。'

/** 筛选下拉选项（固定顺序：人工下发在前）。 */
export const SOURCE_OPTIONS: ReadonlyArray<{ value: Source; label: string }> = [
  { value: 'direct', label: SOURCE_META.direct.label },
  { value: 'proxy', label: SOURCE_META.proxy.label },
]

/**
 * 对话页路由：对话按来源分两个 tab，路径段即来源值（/conversations/direct | proxy），
 * ?conversation= 是打开的对话。缺失或未知来源落人工下发 tab——纯聊天与无 task 的会话归
 * 人工下发侧（与后端列表分流一致）。
 */
export function conversationPath(source: string | undefined, conversationId?: string): string {
  const tab: Source = source === 'proxy' ? 'proxy' : 'direct'
  return conversationId
    ? `/conversations/${tab}?conversation=${encodeURIComponent(conversationId)}`
    : `/conversations/${tab}`
}
