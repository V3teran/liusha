// 漏洞分级 → 颜色 / 排序。统一 Tailwind 400-500 调色板（暗背景清晰、威胁梯度区分度高）：
// 红→橙→黄是危险渐变，low 蓝灰 / info 蓝是低危/信息（冷色与暖色危险区拉开）。供 Findings/执行图等复用。
export const severityColor: Record<string, string> = {
  critical: '#ef4444', // red
  high: '#f97316', // orange
  medium: '#eab308', // amber/yellow
  low: '#94a3b8', // slate（低危，冷灰）
  info: '#3b82f6', // blue（信息）
}

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
