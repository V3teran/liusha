// 发现分级 → 颜色 / 标签 / 排序。单一真相源：发现台账列 / FindingDrawer / FindingCard /
// 探索图节点卡全部从此处取值。
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
export type Severity = (typeof SEVERITIES)[number]

/**
 * 各档的数量里不为 0 的档，按严重程度从高到低排列。展示「按严重度分档的发现数」的地方共用：
 * 为 0 的档不列（列出来只是噪声），顺序也不由数据里键的先后决定。
 */
export function severityEntries(counts: Readonly<Record<string, number | undefined>>): Array<[Severity, number]> {
  return SEVERITIES.filter((s) => (counts[s] ?? 0) > 0).map((s) => [s, counts[s] as number])
}

/**
 * 把等级色当裸文字用时的取色：向正文色混 15%。等级 token 是按「色字 / 自身的淡底徽章」校准的，
 * 直接当裸文字压在选中行的淡底上，深色主题下红色会跌到 AA 以下（实测 4.43）；混一点正文色后，
 * 各档在普通行、悬停行、选中行上都不低于 5.3，色相不变。
 */
export function severityTextColor(sev: string): string {
  return `color-mix(in oklab, ${severityColorOf(sev)} 85%, var(--text))`
}

// 生成标签徽章的三件套配色（淡底 + 同色字 + 同色边），供 FindingCard 徽章用。
// 底与边用 color-mix 派生——token 是 var() 引用，不能再拼十六进制透明度后缀。
export function severityTagColor(sev: string) {
  const c = severityColorOf(sev)
  return {
    color: `color-mix(in srgb, ${c} 13%, var(--surface))`,
    textColor: c,
    borderColor: `color-mix(in srgb, ${c} 33%, var(--surface))`,
  }
}

// 严重度排序权重：critical 最前，未知最后。
export function severityRank(sev: string): number {
  const order: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }
  return order[sev.toLowerCase()] ?? 5
}
