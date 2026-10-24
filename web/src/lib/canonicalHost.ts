/**
 * canonicalHost 预览：与后端 credential.CanonicalHost 同一规则的镜像——
 * 剥 scheme/userinfo/路径，小写主机，**保留端口**（端口参与隔离：不同端口
 * 是不同应用，凭证不跨端口共享）。仅作录入表单的 UX 提示，存储边界仍以
 * 后端归一为唯一权威。
 */
export function canonicalHostPreview(input: string): string {
  let h = input.trim().toLowerCase()
  if (!h) return ''
  const scheme = h.indexOf('://')
  if (scheme >= 0) h = h.slice(scheme + 3)
  const at = h.lastIndexOf('@')
  if (at >= 0) h = h.slice(at + 1)
  const path = h.search(/[/?#]/)
  if (path >= 0) h = h.slice(0, path)
  if (h.startsWith('[')) {
    const close = h.indexOf(']')
    if (close >= 0) return h.slice(0, close + 1).replace(/[[\]]/g, '') + h.slice(close + 1)
  }
  return h.replace(/\.$/, '')
}
