import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TimelineThread } from './TimelineThread'
import { useConversationStore } from '@/stores/conversation'
import type { Message } from '@/api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function userMsg(seq: number, content: string): Message {
  return { Seq: seq, Role: 'user', Kind: 'message', Metadata: null, ...base, Content: content } as Message
}

function reasoningMsg(seq: number, agentName: string, text = '思考'): Message {
  return {
    Seq: seq,
    Role: 'assistant',
    Kind: 'event',
    Metadata: { Kind: 'reasoning', Text: text, AgentName: agentName, ToolName: '', Args: '', Result: '', DurationMs: 0, Err: '', InTokens: 0, OutTokens: 0, LatencyMs: 0 },
    ...base,
  } as Message
}

function toolCallMsg(seq: number, agentName: string): Message {
  return {
    Seq: seq,
    Role: 'tool',
    Kind: 'event',
    Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: 'ls', Result: '', DurationMs: 0, Err: '', AgentName: agentName, Text: '', InTokens: 0, OutTokens: 0, LatencyMs: 0 },
    ...base,
  } as Message
}

describe('TimelineThread', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('渲染 store 中的消息', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    render(<TimelineThread />)
    expect(screen.getByText('扫这个目标')).toBeTruthy()
  })

  it('普通工具调用折叠进 StepTools', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(toolCallMsg(2, 'planner'))
    render(<TimelineThread />)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  // 方案 B：子代理缩进由 AgentCard / StepTools 外层的 pl-6 承载（右缩一档，表达从属调度者）。
  const SUB_INDENT = '.pl-6'

  it('子代理的工具组缩进（pl-6）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '分派')) // 编排模式：存在调度者
    s.ingest(toolCallMsg(3, 'exploitation'))
    const { container } = render(<TimelineThread />)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  it('子代理 reasoning 行带 agent 领头标签 + 缩进（pl-6）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '分派')) // 编排模式：存在调度者
    s.ingest(reasoningMsg(3, 'reconnaissance', '扫端口'))
    const { container } = render(<TimelineThread />)
    expect(screen.getAllByText('侦察').length).toBeGreaterThan(0) // ReasoningCard 内 agent 领头
    expect(container.querySelector(SUB_INDENT)).toBeTruthy()
  })

  it('planner reasoning 行不缩进（无 pl-6）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '分派任务'))
    const { container } = render(<TimelineThread />)
    expect(container.querySelector(SUB_INDENT)).toBeFalsy()
  })

  it('passive 单 agent 模式：无 planner 时主 agent（traffic-analysis）不缩进', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'traffic-analysis', '分析流量'))
    s.ingest(toolCallMsg(3, 'traffic-analysis'))
    const { container } = render(<TimelineThread />)
    // 无调度者 → 不构成主从关系 → 平铺，不出现子代理缩进。
    expect(container.querySelector(SUB_INDENT)).toBeFalsy()
    expect(screen.getAllByText('流量分析').length).toBeGreaterThan(0)
  })

  it('流式推理节点：liveReasoning 设置时展示带 agent 配色的活动节点', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析中…', 'traffic-analysis')
    const { container } = render(<TimelineThread />)
    const card = container.querySelector('[data-card="reasoning"]')
    expect(card).toBeTruthy()
    expect(screen.getAllByText('流量分析').length).toBeGreaterThan(0)
  })

  it('流式推理节点为子代理时也缩进（pl-6）', () => {
    const s = useConversationStore.getState()
    s.ingest(reasoningMsg(1, 'planner', '分派')) // 编排模式：存在调度者
    s.appendReasoningDelta('分析中…', 'exploitation')
    const { container } = render(<TimelineThread />)
    const liveRow = container.querySelector('[data-live-node]')
    expect(liveRow?.querySelector(SUB_INDENT)).toBeTruthy()
  })

  it('流式推理节点为 planner 时不缩进', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('派发任务…', 'planner')
    const { container } = render(<TimelineThread />)
    const liveRow = container.querySelector('[data-live-node]')
    expect(liveRow?.querySelector(SUB_INDENT)).toBeFalsy()
  })

  it('无 liveReasoning 时不渲染活动节点', () => {
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('[data-live-node]')).toBeFalsy()
  })

  it('user 消息走右侧指令块（CommandBlock），无卡外图标', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    const { container } = render(<TimelineThread />)
    // 指令块标题「指令」存在；用户块无卡外图标点（h-[26px] w-[26px]），那是 agent 卡专属。
    expect(screen.getByText('指令')).toBeTruthy()
    expect(screen.getByText('扫这个目标')).toBeTruthy()
    expect(container.querySelector('.h-\\[26px\\].w-\\[26px\\]')).toBeFalsy()
  })

  it('reasoning 等过程节点走 agent 卡（含卡外 26px 图标点）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '分析'))
    const { container } = render(<TimelineThread />)
    // 过程节点保留卡外语义图标点（Brain 等），验证 AgentCard 图标未被误删。
    expect(container.querySelector('.h-\\[26px\\].w-\\[26px\\]')).toBeTruthy()
  })
})
