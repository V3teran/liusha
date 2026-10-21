import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TimelineThread } from './TimelineThread'
import { useConversationStore } from '@/stores/conversation'
import type { Message, ScanEvent } from '@/api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function userMsg(seq: number, content: string): Message {
  return { Seq: seq, Role: 'user', Kind: 'message', Metadata: null, ...base, Content: content } as Message
}

function reasoningMsg(seq: number, agentName: string, text = '思考'): Message {
  return {
    Seq: seq,
    Role: 'assistant',
    Kind: 'event',
    Metadata: { kind: 'reasoning', text, agent_name: agentName, tool_name: '', args: '', result: '', duration_ms: 0, err: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

function toolCallMsg(seq: number, agentName: string): Message {
  return {
    Seq: seq,
    Role: 'tool',
    Kind: 'event',
    Metadata: { kind: 'tool_call', tool_name: 'run_command', args: 'ls', result: '', duration_ms: 0, err: '', agent_name: agentName, text: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

function toolResultMsg(seq: number, agentName: string): Message {
  return {
    Seq: seq,
    Role: 'tool',
    Kind: 'event',
    Metadata: { kind: 'tool_result', tool_name: 'run_command', args: '', result: 'ok', duration_ms: 12, err: '', agent_name: agentName, text: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

const flush = () => new Promise<void>((r) => setTimeout(r, 0))

describe('TimelineThread', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('渲染 store 中的消息', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    render(<TimelineThread />)
    expect(screen.getByText('扫这个目标')).toBeTruthy()
  })

  it('reasoning 终态卡渲染每步用量 chip（metadata 用量驱动，含 k/s 简写）', () => {
    const s = useConversationStore.getState()
    const msg = reasoningMsg(2, 'planner', '规划完成')
    msg.Metadata = { ...msg.Metadata!, in_tokens: 4348, out_tokens: 800, latency_ms: 58882 }
    s.ingest(userMsg(1, '扫这个目标'))
    s.ingest(msg)
    render(<TimelineThread />)
    expect(screen.getByText('↑ 4.3k')).toBeTruthy()
    expect(screen.getByText('↓ 800')).toBeTruthy()
    expect(screen.getByText('⏱ 58.9s')).toBeTruthy()
  })

  it('任务活跃时悬挂 tool_call 组头显示「执行中」；非活跃不显示', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    s.ingest(toolCallMsg(2, 'executor'))
    s.ingest(toolResultMsg(3, 'executor'))
    s.ingest(toolCallMsg(4, 'executor')) // 悬挂：已发 call 未见 result

    const { rerender } = render(<TimelineThread active />)
    expect(screen.getByText('执行中…')).toBeTruthy()

    rerender(<TimelineThread />) // 任务终态：门控关闭，悬挂 call 不转
    expect(screen.queryByText('执行中…')).toBeNull()
  })

  it('普通工具调用折叠进 StepTools', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(toolCallMsg(2, 'planner'))
    render(<TimelineThread />)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  // 四 agent 平级平铺：认知循环是平级协作（bus 广播、互相制衡），不存在主从调度——
  // 身份由配色/图标/领头名承担，任何 agent 的行都不缩进（缩进语言保留给未来真正的
  // orchestrator→subagent 派发场景）。
  it('四 agent 行全部平铺（无主从缩进）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '规划任务'))
    s.ingest(reasoningMsg(3, 'executor', '执行注入测试'))
    s.ingest(toolCallMsg(4, 'executor'))
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('.pl-6')).toBeFalsy()
    expect(screen.getAllByText('执行').length).toBeGreaterThan(0) // ReasoningCard 内 agent 领头
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  it('monitor 行同样平铺且带领头标签', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'monitor', '巡查任务'))
    s.ingest(toolCallMsg(3, 'monitor'))
    const { container } = render(<TimelineThread />)
    expect(container.querySelector('.pl-6')).toBeFalsy()
    expect(screen.getAllByText('监察').length).toBeGreaterThan(0)
  })

  it('流式思考道：增量到达时展示带 agent 配色的活动节点', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析中…', 'evaluator')
    const { container } = render(<TimelineThread />)
    const card = container.querySelector('[data-card="reasoning"]')
    expect(card).toBeTruthy()
    expect(screen.getAllByText('裁决').length).toBeGreaterThan(0)
  })

  it('多条思考道并发渲染（四 agent 并发场景）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划…', 'planner')
    s.appendReasoningDelta('执行…', 'executor')
    const { container } = render(<TimelineThread />)
    expect(container.querySelectorAll('[data-live-node]')).toHaveLength(2)
    expect(screen.getAllByText('规划').length).toBeGreaterThan(0)
    expect(screen.getAllByText('执行').length).toBeGreaterThan(0)
  })

  it('流式活动道同样平铺（无主从缩进）', () => {
    const s = useConversationStore.getState()
    s.ingest(reasoningMsg(1, 'planner', '分派'))
    s.appendReasoningDelta('分析中…', 'executor')
    const { container } = render(<TimelineThread />)
    const liveRow = container.querySelector('[data-live-node]')
    expect(liveRow?.querySelector('.pl-6')).toBeFalsy()
  })

  it('无思考道时不渲染活动节点', () => {
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

describe('RunningIndicator（间隙运行指示）', () => {
  it('active 且无活动道时渲染指示器', () => {
    render(<TimelineThread active />)
    expect(screen.queryByTestId('running') ?? document.querySelector('[data-running-indicator]')).toBeTruthy()
  })

  it('有活动道时让位（道本身即活动信号）', async () => {
    const store = useConversationStore.getState()
    store.appendReasoningDelta('规划中', 'planner')
    render(<TimelineThread active />)
    await flush()
    expect(document.querySelector('[data-running-indicator]')).toBeNull()
    useConversationStore.getState().reset()
  })

  it('非 active（任务未在跑）不渲染', () => {
    render(<TimelineThread />)
    expect(document.querySelector('[data-running-indicator]')).toBeNull()
  })
})

describe('task_status 节点渲染（renderCard 集成）', () => {
  it('completed 节点渲染居中状态卡（含报告字段）', async () => {
    const s = useConversationStore.getState()
    s.ingest({
      ...base, Seq: 1, Role: 'assistant', Kind: 'event',
      Metadata: { kind: 'task_status', status: 'completed', steps: 11, findings: 2, stop_why: 'task_converged' } as ScanEvent,
    })
    render(<TimelineThread />)
    await flush()
    expect(screen.getByText('扫描完成')).toBeTruthy()
    expect(screen.getByText(/11 步/)).toBeTruthy()
    expect(screen.getByText(/2 个漏洞/)).toBeTruthy()
    expect(screen.getByText(/task_converged/)).toBeTruthy()
  })

  it('aborted 节点渲染停止卡并清空全部活动道', async () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('执行中', 'executor')
    s.ingest({
      ...base, Seq: 2, Role: 'assistant', Kind: 'event',
      Metadata: { kind: 'task_status', status: 'aborted', reason: '用户停止' } as ScanEvent,
    })
    render(<TimelineThread />)
    await flush()
    expect(screen.getByText('扫描已停止')).toBeTruthy()
    expect(screen.getByText(/用户停止/)).toBeTruthy()
    expect(document.querySelector('[data-live-node]')).toBeNull()
  })
})
