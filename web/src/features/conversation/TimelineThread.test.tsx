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
    s.ingest(toolCallMsg(2, 'orchestrator'))
    render(<TimelineThread />)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  it('子代理的工具组缩进（ml-[26px]）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(toolCallMsg(2, 'exploitation'))
    const { container } = render(<TimelineThread />)
    const row = container.querySelector('.ml-\\[26px\\]')
    expect(row).toBeTruthy()
    expect(row?.textContent).toContain('1 次工具调用')
  })

  it('orchestrator 的工具组不缩进', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(toolCallMsg(2, 'orchestrator'))
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('.ml-\\[26px\\]')).toBeFalsy()
  })

  it('root 消息（user）不缩进、不带 agent 标签', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('.ml-\\[26px\\]')).toBeFalsy()
  })

  it('子代理 reasoning 行带 agent 标签徽章 + 彩色左边框', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'reconnaissance', '扫端口'))
    const { container } = render(<TimelineThread />)
    expect(screen.getAllByText('侦察').length).toBeGreaterThan(0) // agentLabel('reconnaissance')
    const borderEl = container.querySelector('.border-l-2')
    expect(borderEl).toBeTruthy()
    expect((borderEl as HTMLElement).style.borderColor).toBeTruthy()
  })

  it('orchestrator reasoning 行不带行级 agent 标签徽章（不缩进，无竖条）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'orchestrator', '分派任务'))
    const { container } = render(<TimelineThread />)
    // 行级缩进容器（sub && '-ml-0.5 border-l-2 pl-3'）不存在——orchestrator 不算子代理。
    expect(container.querySelector('.border-l-2')).toBeFalsy()
  })

  it('流式推理气泡：liveReasoning 设置时展示带 agent 配色的活动节点', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析中…', 'traffic-analysis')
    const { container } = render(<TimelineThread />)
    const card = container.querySelector('[data-card="reasoning"]')
    expect(card).toBeTruthy()
    expect(screen.getAllByText('流量分析').length).toBeGreaterThan(0)
  })

  it('流式推理气泡为子代理时也缩进 + 带边框', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析中…', 'exploitation')
    const { container } = render(<TimelineThread />)
    const liveRow = container.querySelector('[aria-hidden="true"]')
    expect(liveRow?.className).toContain('ml-[26px]')
  })

  it('流式推理气泡为 orchestrator 时不缩进', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('派发任务…', 'orchestrator')
    const { container } = render(<TimelineThread />)
    const liveRow = container.querySelector('[aria-hidden="true"]')
    expect(liveRow?.className).not.toContain('ml-[26px]')
  })

  it('无 liveReasoning 时不渲染活动节点', () => {
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('[aria-hidden="true"]')).toBeFalsy()
  })
})
