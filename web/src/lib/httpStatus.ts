// HTTP 语义配色：状态码按类别映射到 severity 色板（与全站 --sev-* token 一致），
// method 按「读/写/危险」语义分档。列表徽章与详情头共用，单点定义避免各处手搓颜色。

/** 状态码类别色：2xx 成功/绿、3xx 跳转/中性、4xx 客户端错/黄、5xx 服务端错/红、其它/灰。 */
export function statusColor(code: number): string {
  if (code >= 200 && code < 300) return 'var(--sev-low)'
  if (code >= 300 && code < 400) return 'var(--accent)'
  if (code >= 400 && code < 500) return 'var(--sev-medium)'
  if (code >= 500) return 'var(--sev-critical)'
  return 'var(--muted)'
}

/** method 语义色：GET/HEAD 读=中性，POST/PUT/PATCH 写=强调，DELETE 危险=红。 */
export function methodColor(method: string): string {
  const m = method.toUpperCase()
  if (m === 'DELETE') return 'var(--sev-critical)'
  if (m === 'POST' || m === 'PUT' || m === 'PATCH') return 'var(--accent)'
  return 'var(--muted)'
}
