import type { Message } from '../api/types'

// 会话卡片分类——单一真相源，TimelineThread（agent 卡渲染分发）与 threadRows（按步分组折叠）共用，避免逻辑分叉。
// 事件面（后端写者）：reasoning（agent 每轮终稿）、task_status（任务生命周期节点）、
// tool_call/tool_result（工具过程事件，执行前后各一条）、
// finding（坐实发现：晋升 result 的主张经 ClaimSink 落库即发，中性领域词）。
export type MessageKindTag =
  | 'user'
  | 'assistant'
  | 'reasoning'
  | 'task-status'
  | 'finding'
  | 'tool-call'
  | 'tool-result'

export function classifyMessage(m: Message): MessageKindTag {
  if (m.kind === 'message') return m.role === 'user' ? 'user' : 'assistant'
  const ev = m.metadata
  if (!ev) return 'assistant'
  if (ev.kind === 'reasoning') return 'reasoning'
  if (ev.kind === 'task_status') return 'task-status'
  if (ev.kind === 'finding') return 'finding'
  if (ev.kind === 'tool_call') return 'tool-call'
  if (ev.kind === 'tool_result') return 'tool-result'
  return 'assistant'
}

// 普通工具调用（tool-call / tool-result）——会被折叠进步组；其余（推理/状态/会话）独立成行。
export function isCollapsibleTool(tag: MessageKindTag): boolean {
  return tag === 'tool-call' || tag === 'tool-result'
}
