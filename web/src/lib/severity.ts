// 漏洞分级 → 颜色 / 标签 / 排序。单一真相源：FindingsPage / FindingDrawer / FindingCard /
// SitemapPage / 执行图节点全部从此处取值，不再各自维护一份重复的 SEV_LABEL/severityColor。
// 统一 Tailwind 400-500 调色板（暗背景清晰、威胁梯度区分度高）：
// 红→橙→黄是危险渐变，low 蓝灰 / info 蓝是低危/信息（冷色与暖色危险区拉开）。
export const severityColor: Record<string, string> = {
  critical: '#ef4444', // red
  high: '#f97316', // orange
  medium: '#eab308', // amber/yellow
  low: '#94a3b8', // slate（低危，冷灰）
  info: '#3b82f6', // blue（信息）
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
export function severityTagColor(sev: string) {
  const c = severityColor[sev.toLowerCase()] ?? '#6e7681'
  return { color: c + '22', textColor: c, borderColor: c + '55' }
}

// 严重度排序权重：critical 最前，未知最后。
export function severityRank(sev: string): number {
  const order: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }
  return order[sev.toLowerCase()] ?? 5
}
