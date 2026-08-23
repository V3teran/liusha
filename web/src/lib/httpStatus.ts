// HTTP 语义配色：状态码/方法映射到设计 token 色板。列表徽章与详情头共用，单点定义避免各处手搓颜色。
//
// 取色原则（对齐 Burp / DevTools 的密集请求表实践）：颜色承载语义，不做装饰。
// - 状态：成功=绿（成功是常态里唯一值得肯定的信号）、跳转=中性、客户端错=琥珀、服务端错=红。
// - 方法：写操作（会改状态，渗透视角最值得关注）=蓝、破坏性 DELETE=红、读操作=中性。
//   刻意不给方法用品牌强调色（emerald/accent）——那是交互/选中态的专用色，避免语义稀释。

/** 状态码类别色：2xx 成功/绿、3xx 跳转/中性、4xx 客户端错/琥珀、5xx 服务端错/红、其它/中性。 */
export function statusColor(code: number): string {
  if (code >= 500) return 'var(--sev-critical)'
  if (code >= 400) return 'var(--sev-medium)'
  if (code >= 300) return 'var(--muted)'
  if (code >= 200) return 'var(--accent)'
  return 'var(--muted)'
}

/** method 语义色：DELETE 破坏性=红、POST/PUT/PATCH 写=蓝、GET/HEAD/OPTIONS 读=中性。 */
export function methodColor(method: string): string {
  const m = method.toUpperCase()
  if (m === 'DELETE') return 'var(--sev-critical)'
  if (m === 'POST' || m === 'PUT' || m === 'PATCH') return 'var(--sev-info)'
  return 'var(--muted)'
}
