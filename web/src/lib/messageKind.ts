import type { Message } from '../api/types'

// 会话卡片分类——单一真相源，TimelineThread（导轨渲染分发）与 threadRows（按步分组折叠）共用，避免逻辑分叉。
//
// write_finding 一次产生两条事件：tool_call（带 Args=漏洞详情）+ tool_result（仅 {id}）。
// → tool_call 渲染 finding 卡（须含 summary，否则是残缺重发→hidden）；tool_result 隐藏。
// task（派发）的 tool_result = 派发完成（带子代理执行总时长）→ spawn-done。
export type MessageKindTag =
  | 'user'
  | 'assistant'
  | 'reasoning'
  | 'spawn'
  | 'spawn-done'
  | 'tool-call'
  | 'tool-result'
  | 'finding'
  | 'compaction'
  | 'hidden'

export function classifyMessage(m: Message): MessageKindTag {
  if (m.Kind === 'message') return m.Role === 'user' ? 'user' : 'assistant'
  const ev = m.Metadata
  if (!ev) return 'assistant'
  if (ev.Kind === 'reasoning') return 'reasoning'
  if (ev.Kind === 'spawn') return 'spawn'
  if (ev.Kind === 'compaction') return 'compaction'
  if (ev.ToolName === 'write_finding') {
    if (ev.Kind !== 'tool_call' || ev.Err) return 'hidden'
    try {
      if (!JSON.parse(ev.Args || '{}').summary) return 'hidden'
    } catch {
      return 'hidden'
    }
    return 'finding'
  }
  if (ev.ToolName === 'task' && ev.Kind === 'tool_result') return 'spawn-done'
  if (ev.Kind === 'tool_call') return 'tool-call'
  return 'tool-result'
}

// 普通工具调用（tool-call / tool-result）——会被折叠进步组；其余（漏洞/派发/派发完成/想/会话）留在外面。
export function isCollapsibleTool(tag: MessageKindTag): boolean {
  return tag === 'tool-call' || tag === 'tool-result'
}
