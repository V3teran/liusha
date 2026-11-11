// LLM 消息/结果解析：从 LlmInvocationDrawer 抽出的纯函数，脱离 JSX 可独立单测。
// messages 与 result 是框架层 llm.Message / llm.Response 的 JSON（审计直出契约，小写 json tag 键）。

export interface MsgView {
  role: string
  text: string
  calls: import('./toolCalls').ToolCallView[]
}

import { toToolCalls } from './toolCalls'
import { catColor, type CategoricalHue } from '@/lib/categoricalColor'

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
    calls: toToolCalls(o.tool_calls),
  }
}

/** 模型思考过程：审计契约键 result.thinking.text（未开启扩展思考时为空串）。 */
export function extractReasoning(result: unknown): string {
  if (!result || typeof result !== 'object') return ''
  const thinking = (result as { thinking?: { text?: unknown } }).thinking
  return typeof thinking?.text === 'string' ? thinking.text : ''
}

// 消息 role 配色：区分 system/user/assistant/tool，让长对话可扫视（分类色 token，随主题）。
const MSG_ROLE_HUE: Record<string, CategoricalHue> = {
  system: 'neutral',
  user: 'sky',
  assistant: 'emerald',
  tool: 'violet',
}

export function msgColor(role: string): string {
  return catColor(MSG_ROLE_HUE[role] ?? 'neutral')
}
