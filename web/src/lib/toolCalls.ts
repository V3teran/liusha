// LLM 工具调用（tool_calls / function_call）的规范化：把 OpenAI 风格的原始结构
// 拆成「工具名 + 美化参数」，供审计详情抽屉结构化渲染（取代裸 JSON.stringify）。

/** 单次工具调用的结构化视图。 */
export interface ToolCallView {
  id: string
  name: string
  args: string // 美化后的参数 JSON；空参数为空串；非法 JSON 原样保留
}

/**
 * OpenAI tool_calls 的 arguments 是「JSON 字符串」，裸展示是一行转义串，读不了。
 * 解析后按 2 空格缩进重排；空对象归一成空串；非法 JSON（罕见）原样保留，不吞内容。
 */
export function prettyArgs(raw: unknown): string {
  if (typeof raw !== 'string') return raw == null ? '' : JSON.stringify(raw, null, 2)
  const s = raw.trim()
  if (!s || s === '{}') return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return raw
  }
}

/**
 * 把 tool_calls（数组）或 function_call（单个）归一成结构化卡片数组。
 * 输入消息与返回结果共用。字段缺失时兜底占位，不抛错。
 */
export function toToolCalls(src: unknown): ToolCallView[] {
  if (src == null) return []
  const arr = Array.isArray(src) ? src : [src]
  return arr.map((c, i) => {
    const o = (c ?? {}) as Record<string, unknown>
    const fn = (o.function ?? o) as Record<string, unknown>
    return {
      id: typeof o.id === 'string' ? o.id : `call-${i}`,
      name: typeof fn.name === 'string' ? fn.name : '(未命名工具)',
      args: prettyArgs(fn.arguments),
    }
  })
}
