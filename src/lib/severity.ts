// 漏洞分级 → 颜色 / 排序。与 style.css 的 --sev-* token 对齐，供 Findings/Sitemap 页复用。
export const severityColor: Record<string, string> = {
  critical: '#f85149',
  high: '#ff7b35',
  medium: '#d29922',
  low: '#6e7681',
  info: '#58a6ff',
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
