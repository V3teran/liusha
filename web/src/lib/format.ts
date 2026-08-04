// 时间与度量格式化：会话卡片时间戳 + 会话总计（token / 耗时）。

const pad = (n: number): string => String(n).padStart(2, '0')

/** 卡片角标：时:分:秒。无效输入返回空串。 */
export function clockTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/**
 * 紧凑日期时间 MM-DD HH:MM:SS（表格时刻列用）。
 * 省年份而非省日期：审计表内跨天是真实情形（实测有 task 从 7/17 跨到 7/18），
 * 只显时分秒会让时序误读；年份则可由悬停 fullTime 补足。
 */
export function shortDateTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 完整日期 YYYY-MM-DD（审计表时刻列首行用，年份不再藏进悬停）。 */
export function dateOnly(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 悬停 title：完整日期时间 YYYY-MM-DD HH:MM:SS。 */
export function fullTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 干活耗时（ms → 人类可读）：>=1h 用 X时Y分；>=1min 用 X分Y秒；否则 X.X秒。 */
export function humanDuration(ms: number): string {
  if (!ms || ms < 0) return '0秒'
  const totalSec = Math.round(ms / 1000)
  if (totalSec < 60) return `${(ms / 1000).toFixed(1)}秒`
  const h = Math.floor(totalSec / 3600)
  const m = Math.floor((totalSec % 3600) / 60)
  const s = totalSec % 60
  if (h > 0) return m ? `${h}时${m}分` : `${h}时`
  return s ? `${m}分${s}秒` : `${m}分`
}

/** token 总数（千分位分隔，精确值——用于 tooltip）。 */
export function humanTokens(n: number): string {
  if (!n || n < 0) return '0'
  return n.toLocaleString('en-US')
}

/** 紧凑数字（chip 展示）：5249357 → "5.25M"、12345 → "12.3K"。 */
export function compactNumber(n: number): string {
  if (!n || n < 0) return '0'
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(n)
}

/** 本地日期键 YYYY-MM-DD（按天分组用，无效输入返回空串）。 */
export function dayKey(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 相对时间：刚刚 / N分钟前 / N小时前 / 昨天 / YYYY-MM-DD（对齐 ChatGPT/Claude 列表惯例）。 */
export function relativeTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const diffMs = Date.now() - d.getTime()
  const min = Math.floor(diffMs / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24 && dayKey(iso) === dayKey(new Date().toISOString())) return `${hr}小时前`
  return dayLabel(iso) // 跨天 → 昨天 / 日期
}

/** 按天分隔条标签：今天 / 昨天 / YYYY-MM-DD（绝对日期统一 ISO，不用中文年月日）。 */
export function dayLabel(iso: string): string {
  const key = dayKey(iso)
  if (!key) return ''
  const today = dayKey(new Date().toISOString())
  const yest = dayKey(new Date(Date.now() - 86400000).toISOString())
  if (key === today) return '今天'
  if (key === yest) return '昨天'
  return key // YYYY-MM-DD（dayKey 已是本地 ISO 日期）
}
