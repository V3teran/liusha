// Burp 式整条 HTTP 报文（起始行 + 头 + 体一体）的展示辅助。
// 后端已把请求/响应各存为单一 raw 文本（唯一存储，无拆分冗余）；前端只做「原文 / 美化」两态切换。

/** 报文头体分界：首个空行（CRLF 优先，兼容纯 LF）。返回 [head, body]；无分界则 body 为空。 */
export function splitHeadBody(raw: string): [string, string] {
  const crlf = raw.indexOf('\r\n\r\n')
  if (crlf >= 0) return [raw.slice(0, crlf), raw.slice(crlf + 4)]
  const lf = raw.indexOf('\n\n')
  if (lf >= 0) return [raw.slice(0, lf), raw.slice(lf + 2)]
  return [raw, '']
}

/** 从整条报文头段解析 Content-Type 值（字段名大小写不敏感，保留参数如 charset）。无则空串。
 *  每条报文自带 Content-Type，故请求/响应各自据此判断能否美化，不依赖外部字段（更 Burp、更准）。 */
export function contentTypeFromRaw(raw: string): string {
  const [head] = splitHeadBody(raw)
  for (const line of head.split(/\r?\n/)) {
    const idx = line.indexOf(':')
    if (idx < 0) continue
    if (line.slice(0, idx).trim().toLowerCase() === 'content-type') {
      return line.slice(idx + 1).trim()
    }
  }
  return ''
}

/** content_type 是否 JSON 家族（application/json、+json 后缀等）。 */
export function isJsonContentType(contentType: string): boolean {
  const ct = contentType.toLowerCase()
  return ct === 'application/json' || ct.endsWith('+json') || ct.startsWith('application/json')
}

// prettyRaw 把整条报文的 body 段按 content_type 美化，头段原样保留。
// 仅在 body 能被安全解析时才改写；解析失败原样返回，绝不吞掉或伪造内容。
export function prettyRaw(raw: string, contentType: string): string {
  if (!raw) return raw
  const [head, body] = splitHeadBody(raw)
  if (!body.trim()) return raw
  if (!isJsonContentType(contentType)) return raw
  try {
    const pretty = JSON.stringify(JSON.parse(body), null, 2)
    return `${head}\r\n\r\n${pretty}`
  } catch {
    return raw // 非法 JSON：保底原文，不做破坏性改写
  }
}

/** 该报文是否有可美化的内容（JSON body 存在）——决定是否显示「美化」切换。 */
export function canPretty(raw: string, contentType: string): boolean {
  if (!raw || !isJsonContentType(contentType)) return false
  const [, body] = splitHeadBody(raw)
  return body.trim().length > 0
}
