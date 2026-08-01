// LLM 消息/结果解析：从 LlmInvocationDrawer 抽出的纯函数，脱离 JSX 可独立单测。
// messages 是 OpenAI 风格消息数组；result 是模型返回，结构不定（provider 差异 + 是否含 reasoning/tool_calls）。

export interface MsgView {
  role: string
  text: string
  calls: import('./toolCalls').ToolCallView[]
}

import { toToolCalls } from './toolCalls'

/** content 可能是纯字符串，也可能是多模态数组 [{type:'text',...}, {type:'image_url',...}]。取文本片段。 */
export function contentToText(c: unknown): string {
  if (typeof c === 'string') return c
  if (Array.isArray(c)) {
    return c
      .map((part) => {
        if (typeof part === 'string') return part
        const p = part as Record<string, unknown>
        if (typeof p.text === 'string') return p.text
        return p.type ? `[${String(p.type)}]` : ''
      })
      .filter(Boolean)
      .join('\n')
  }
  if (c == null) return ''
  return JSON.stringify(c, null, 2)
}

/** 把消息数组中的一条原始对象规整成结构化视图（角色 + 正文 + 工具调用）。 */
export function toMsgView(m: unknown): MsgView {
  const o = (m ?? {}) as Record<string, unknown>
  return {
    role: typeof o.role === 'string' ? o.role : 'unknown',
    text: contentToText(o.content),
    calls: toToolCalls(o.tool_calls ?? o.function_call),
  }
}

/**
 * 模型思考过程：字段名在不同 provider/版本间不稳定，已知两种变体：
 * result.reasoning_content 或 result.extra['reasoning-content']。都没有则返回空串。
 */
export function extractReasoning(result: unknown): string {
  if (!result || typeof result !== 'object') return ''
  const o = result as Record<string, unknown>
  if (typeof o.reasoning_content === 'string') return o.reasoning_content
  const extra = o.extra as Record<string, unknown> | undefined
  const alt = extra?.['reasoning-content']
  return typeof alt === 'string' ? alt : ''
}

// 消息 role 配色：区分 system/user/assistant/tool，让长对话可扫视。
const MSG_ROLE_COLOR: Record<string, string> = {
  system: '#94a3b8',
  user: '#38bdf8',
  assistant: '#34d399',
  tool: '#a78bfa',
}

export function msgColor(role: string): string {
  return MSG_ROLE_COLOR[role] ?? '#94a3b8'
}
