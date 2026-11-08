// 漏洞分级 → 颜色 / 标签 / 排序。单一真相源：FindingsPage / FindingDrawer / FindingCard /
// 执行图节点全部从此处取值，不再各自维护一份重复的 SEV_LABEL/severityColor。
// 颜色引用设计 token（design-tokens.css 的 --sev-*，深浅主题各自校准到 WCAG AA），
// 返回 CSS 值字符串，可直接用于 style 与 color-mix()。
// 色序对齐业界共识（critical 红、high 橙、medium 黄、low 绿、info 蓝）——
// 低危绿承载「风险可接受」语义，冷色蓝留给信息类。
export const severityColor: Record<string, string> = {
  critical: 'var(--sev-critical)',
  high: 'var(--sev-high)',
  medium: 'var(--sev-medium)',
  low: 'var(--sev-low)',
  info: 'var(--sev-info)',
}

/** 未知等级的兜底色（中性灰，随主题）。 */
export const SEVERITY_FALLBACK_COLOR = 'var(--status-neutral)'

/** 按等级取色，未知等级落中性灰。 */
export function severityColorOf(sev: string): string {
  return severityColor[sev.toLowerCase()] ?? SEVERITY_FALLBACK_COLOR
}

// 中文标签。未知 severity 兜底显示原始值（调用处 severityLabel(sev) || sev）。
export const severityLabel: Record<string, string> = {
  critical: '严重',
  high: '高危',
  medium: '中危',
  low: '低危',
  info: '信息',
}

export const SEVERITIES = ['critical', 'high', 'medium', 'low', 'info'] as const

// 生成标签徽章的三件套配色（淡底 + 同色字 + 同色边），供 FindingCard 徽章用。
// 底与边用 color-mix 派生——token 是 var() 引用，不能再拼十六进制透明度后缀。
export function severityTagColor(sev: string) {
  const c = severityColorOf(sev)
  return {
    color: `color-mix(in srgb, ${c} 13%, transparent)`,
    textColor: c,
    borderColor: `color-mix(in srgb, ${c} 33%, transparent)`,
  }
}

// 严重度排序权重：critical 最前，未知最后。
export function severityRank(sev: string): number {
  const order: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }
  return order[sev.toLowerCase()] ?? 5
}
