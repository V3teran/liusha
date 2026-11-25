import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TimelineThread } from './TimelineThread'
import { useConversationStore } from '@/stores/conversation'
import type { Message, TimelineEventMeta } from '@/api/types'

const base = { conversation_id: 'c', created_at: '', content: '' }

function userMsg(seq: number, content: string): Message {
  return { seq: seq, role: 'user', kind: 'message', metadata: null, ...base, content: content } as Message
}

function reasoningMsg(seq: number, agentName: string, text = '思考'): Message {
  return {
    seq: seq,
    role: 'assistant',
    kind: 'event',
    metadata: { kind: 'reasoning', text, agent_name: agentName, tool_name: '', args: '', result: '', duration_ms: 0, err: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

function toolCallMsg(seq: number, agentName: string): Message {
  return {
    seq: seq,
    role: 'assistant',
    kind: 'event',
    metadata: { kind: 'tool_call', tool_name: 'command_run', args: 'ls', result: '', duration_ms: 0, err: '', agent_name: agentName, text: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

function toolResultMsg(seq: number, agentName: string): Message {
  return {
    seq: seq,
    role: 'assistant',
    kind: 'event',
    metadata: { kind: 'tool_result', tool_name: 'command_run', args: '', result: 'ok', duration_ms: 12, err: '', agent_name: agentName, text: '', in_tokens: 0, out_tokens: 0, latency_ms: 0, thinking: '' },
    ...base,
  } as Message
}

const flush = () => new Promise<void>((r) => setTimeout(r, 0))
// 用例不关心筛选：不选任何智能体
const unfiltered = { agents: [] as string[], onAgentsChange: () => {} }

describe('TimelineThread', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('渲染 store 中的消息', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    render(<TimelineThread {...unfiltered} />)
    expect(screen.getByText('扫这个目标')).toBeTruthy()
  })

  it('reasoning 终态卡渲染每步用量 chip（metadata 用量驱动，含 k/s 简写）', () => {
    const s = useConversationStore.getState()
    const msg = reasoningMsg(2, 'planner', '规划完成')
    msg.metadata = { ...msg.metadata!, in_tokens: 4348, out_tokens: 800, latency_ms: 58882 }
    s.ingest(userMsg(1, '扫这个目标'))
    s.ingest(msg)
    render(<TimelineThread {...unfiltered} />)
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

    const { rerender } = render(<TimelineThread {...unfiltered} active />)
    expect(screen.getByText('执行中…')).toBeTruthy()

    rerender(<TimelineThread {...unfiltered} />) // 任务终态：门控关闭，悬挂 call 不转
    expect(screen.queryByText('执行中…')).toBeNull()
  })

  it('工具调用折叠成一组，放在它所属智能体的卡里（还没出终稿的轮也在卡里，卡带可读的身份）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(toolCallMsg(2, 'planner'))
    render(<TimelineThread {...unfiltered} />)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
    // 身份不只靠颜色与图标：在途卡里没有文字结论，靠头像的可读名认出是规划者
    expect(screen.getByRole('img', { name: '规划（planner）' })).toBeTruthy()
  })

  // 四 agent 平级平铺：认知循环是平级协作（bus 广播、互相制衡），不存在主从调度——
  // 身份由配色/图标/领头名承担，任何 agent 的行都不缩进（缩进语言保留给未来真正的
  // orchestrator→subagent 派发场景）。
  it('聊天回答的思考与正文在同一张 agent 卡内（与任务消息同构）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '你好'))
    s.ingest({
      ...base,
      seq: 2,
      role: 'assistant',
      kind: 'message',
      content: '你好，我是安全测试助手',
      metadata: { thinking: '用户在打招呼，简单回应' },
    } as Message)
    render(<TimelineThread {...unfiltered} />)
    // 同一张卡：思考折叠与正文同处一个气泡容器，身份头随卡展示
    const fold = screen.getByText('内部思考').closest('details')
    expect(fold).toBeTruthy()
    expect(fold!.parentElement!.textContent).toContain('你好，我是安全测试助手')
    expect(within(fold!.parentElement!).getByText('助手')).toBeTruthy()
  })

  it('四 agent 行全部平铺（无主从缩进）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '规划任务'))
    s.ingest(reasoningMsg(3, 'executor', '执行注入测试'))
    s.ingest(toolCallMsg(4, 'executor'))
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelector('.pl-6')).toBeFalsy()
    expect(screen.getAllByText('执行').length).toBeGreaterThan(0) // ReasoningCard 内 agent 领头
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  it('monitor 行同样平铺且带领头标签', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'monitor', '巡查任务'))
    s.ingest(toolCallMsg(3, 'monitor'))
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelector('.pl-6')).toBeFalsy()
    expect(screen.getAllByText('监察').length).toBeGreaterThan(0)
  })

  it('流式思考道：增量到达时展示带 agent 配色的活动节点', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('分析中…', 'evaluator')
    const { container } = render(<TimelineThread {...unfiltered} />)
    const card = container.querySelector('[data-card="reasoning"]')
    expect(card).toBeTruthy()
    expect(screen.getAllByText('评估').length).toBeGreaterThan(0)
  })

  it('多条思考道并发渲染（四 agent 并发场景）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('规划…', 'planner')
    s.appendReasoningDelta('执行…', 'executor')
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelectorAll('[data-live-node]')).toHaveLength(2)
    expect(screen.getAllByText('规划').length).toBeGreaterThan(0)
    expect(screen.getAllByText('执行').length).toBeGreaterThan(0)
  })

  it('流式活动道同样平铺（无主从缩进）', () => {
    const s = useConversationStore.getState()
    s.ingest(reasoningMsg(1, 'planner', '分派'))
    s.appendReasoningDelta('分析中…', 'executor')
    const { container } = render(<TimelineThread {...unfiltered} />)
    const liveRow = container.querySelector('[data-live-node]')
    expect(liveRow?.querySelector('.pl-6')).toBeFalsy()
  })

  // axe 实测（aria-hidden-focus）：流式正文按 Markdown 渲染，其中的 URL 是可聚焦链接；
  // 整条思考道曾被 aria-hidden，键盘能 Tab 进读屏「不存在」的内容。
  it('流式思考道不对读屏隐藏，内含链接可正常访问', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('访问 http://192.168.71.25:38080/search 观察回显', 'executor')
    const { container } = render(<TimelineThread {...unfiltered} />)
    const live = container.querySelector('[data-live-node]')!
    expect(live.closest('[aria-hidden="true"]')).toBeNull()
    expect(live.querySelector('[aria-hidden="true"] a, [aria-hidden="true"] button')).toBeNull()
  })

  it('无思考道时不渲染活动节点', () => {
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelector('[data-live-node]')).toBeFalsy()
  })

  it('user 消息走右侧指令块（CommandBlock），无卡外图标', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '扫这个目标'))
    const { container } = render(<TimelineThread {...unfiltered} />)
    // 指令块标题「指令」存在；用户块无卡外图标点（h-[26px] w-[26px]），那是 agent 卡专属。
    expect(screen.getByText('指令')).toBeTruthy()
    expect(screen.getByText('扫这个目标')).toBeTruthy()
    expect(container.querySelector('.h-\\[26px\\].w-\\[26px\\]')).toBeFalsy()
  })

  it('reasoning 等过程节点走 agent 卡（含卡外 26px 图标点）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, 'hi'))
    s.ingest(reasoningMsg(2, 'planner', '分析'))
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelector('.h-8.w-8')).toBeTruthy()
  })
})

// 「任务在跑」的信号在顶栏：状态文字与逐秒走的耗时。轨迹里不再另放一枚「智能体工作中」——
// 与顶栏重复，占着轨迹最下面一行。
describe('轨迹里不重复「工作中」的指示', () => {
  it('任务在跑、哪怕此刻没有在途的卡，也不渲染指示', () => {
    useConversationStore.getState().reset()
    const { container } = render(<TimelineThread {...unfiltered} active />)
    expect(document.querySelector('[data-running-indicator]')).toBeNull()
    expect(within(container).queryByText('智能体工作中')).toBeNull()
    useConversationStore.getState().reset()
  })
})

describe('task_status 节点渲染（renderCard 集成）', () => {
  it('completed 节点渲染居中状态卡（含报告字段）', async () => {
    const s = useConversationStore.getState()
    s.ingest({
      ...base, seq: 1, role: 'assistant', kind: 'event',
      metadata: { kind: 'task_status', status: 'completed', steps: 11, findings: 2, stop_reason: 'task_converged' } as TimelineEventMeta,
    })
    render(<TimelineThread {...unfiltered} />)
    await flush()
    expect(screen.getByText('任务完成')).toBeTruthy()
    expect(screen.getByText(/11 步/)).toBeTruthy()
    expect(screen.getByText(/2 个发现/)).toBeTruthy()
  })

  it('aborted 节点渲染停止卡并清空全部活动道', async () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('执行中', 'executor')
    s.ingest({
      ...base, seq: 2, role: 'assistant', kind: 'event',
      metadata: { kind: 'task_status', status: 'aborted', reason: '用户停止' } as TimelineEventMeta,
    })
    render(<TimelineThread {...unfiltered} />)
    await flush()
    expect(screen.getByText('任务已停止')).toBeTruthy()
    expect(screen.getByText(/用户停止/)).toBeTruthy()
    expect(document.querySelector('[data-live-node]')).toBeNull()
  })
})

describe('一轮一张卡：工具调用归在它那一轮的结论名下', () => {
  beforeEach(() => useConversationStore.getState().reset())

  // 一轮的卡由外到内：身份（谁的、第几步、用了多少）→ 做了什么（工具调用）→ 想了什么（内部思考）
  // → 得出什么（结论）。工具调用是「得出这一条结论」的一步，不是上一条结论的延伸，所以它排在
  // 自己那条结论之前；而身份排在最前——先知道这是谁的卡，再看它做了什么。
  it('一轮的卡：身份在最前，其后是工具调用，最后是结论', () => {
    const s = useConversationStore.getState()
    s.ingest(toolCallMsg(1, 'planner'))
    s.ingest(toolResultMsg(2, 'planner'))
    const concluded = reasoningMsg(3, 'planner', '规划完成')
    concluded.metadata = { ...concluded.metadata!, thinking: '先想一下' }
    s.ingest(concluded)
    const { container } = render(<TimelineThread {...unfiltered} />)
    const card = container.querySelector('[role="log"]') as HTMLElement
    const identity = within(card).getByText('规划')
    const tools = within(card).getByRole('button', { name: /1 次工具调用/ })
    const thinking = within(card).getByText('内部思考')
    const conclusion = within(card).getByText('规划完成')
    const before = (a: Element, b: Element) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
    expect(before(identity, tools)).toBe(true)
    expect(before(tools, thinking)).toBe(true)
    expect(before(thinking, conclusion)).toBe(true)
    // 工具与结论同属这一张卡
    expect(container.querySelectorAll('.animate-slide-up')).toHaveLength(1)
  })

  it('同一智能体并行的两轮各领各的工具事件（按轮次标识）', () => {
    const s = useConversationStore.getState()
    const inRound = (m: Message, id: string) => {
      m.metadata!.round_id = id
      return m
    }
    s.ingest(inRound(toolCallMsg(1, 'executor'), 'a'))
    s.ingest(inRound(toolCallMsg(2, 'executor'), 'b'))
    s.ingest(inRound(toolCallMsg(3, 'executor'), 'b'))
    s.ingest(inRound(toolResultMsg(4, 'executor'), 'b'))
    s.ingest(inRound(reasoningMsg(5, 'executor', '动作 A 完成'), 'a'))
    const { container } = render(<TimelineThread {...unfiltered} />)
    const [concluded, inFlight] = [...container.querySelectorAll('.animate-slide-up')] as HTMLElement[]
    // 先出终稿的 a 轮只领走自己的 1 次调用；b 轮的调用不因为同属执行者而被算进来
    expect(within(concluded!).getByRole('button', { name: /1 次工具调用/ })).toBeTruthy()
    expect(within(concluded!).getByText(/动作 A 完成/)).toBeTruthy()
    expect(within(inFlight!).getByRole('button', { name: /2 次工具调用/ })).toBeTruthy()
    expect(within(inFlight!).queryByText(/动作 A 完成/)).toBeNull()
  })

  it('在途的轮与同一智能体的流式道在同一张卡里', () => {
    const s = useConversationStore.getState()
    s.ingest(toolCallMsg(1, 'executor'))
    s.appendReasoningDelta('正在探测目标…', 'executor')
    const { container } = render(<TimelineThread {...unfiltered} active />)
    const live = container.querySelectorAll('[data-live-node]')
    expect(live).toHaveLength(1)
    const card = live[0] as HTMLElement
    expect(within(card).getByRole('button', { name: /1 次工具调用/ })).toBeTruthy()
    expect(card.querySelector('[data-card="reasoning"]')).toBeTruthy() // 流式思考的正文（打字机逐字出，这里只看它在同一张卡里）
  })

  it('展开状态在一轮出终稿后保留（折叠组换了位置，正在读的内容不折回去）', async () => {
    const user = userEvent.setup()
    const s = useConversationStore.getState()
    s.ingest(toolCallMsg(1, 'executor'))
    const { rerender } = render(<TimelineThread {...unfiltered} active />)
    await user.click(screen.getByRole('button', { name: /1 次工具调用/ }))
    expect(screen.getByText('command_run')).toBeTruthy() // 展开后露出调用卡

    act(() => useConversationStore.getState().ingest(reasoningMsg(2, 'executor', '执行完成')))
    rerender(<TimelineThread {...unfiltered} active />)
    expect(screen.getByRole('button', { name: /1 次工具调用/ }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByText('执行完成')).toBeTruthy()
  })

  it('任务结束后仍没有终稿的轮，组头说明它没有结论', () => {
    const s = useConversationStore.getState()
    s.ingest(toolCallMsg(1, 'executor'))
    s.ingest(toolResultMsg(2, 'executor'))
    render(<TimelineThread {...unfiltered} />)
    expect(screen.getByText('未产出结论')).toBeTruthy()
  })
})

// 一条发现是「评估者坐实了一条主张」这个事实的可见形态：由复现门在晋升时发出，事件里记的就是
// 评估者。它归评估者——筛选评估者时出现、计入它的条目数，筛选别的智能体时不出现。
// 卡本身不套 agent 卡：它按严重度着色，套上会多出一个头像、把结论与发现混成一种东西。
describe('坐实的发现归评估者', () => {
  beforeEach(() => useConversationStore.getState().reset())

  const finding = (seq: number): Message =>
    ({
      ...base,
      seq,
      role: 'assistant',
      kind: 'event',
      content: '',
      metadata: { kind: 'finding', agent_name: 'evaluator', summary: '登录页缺少安全响应头', severity: 'low', host: 'example.com', target: 'https://example.com/' },
    }) as Message

  it('发现卡不套 agent 卡、不带头像', () => {
    useConversationStore.getState().ingest(finding(1))
    const { container } = render(<TimelineThread {...unfiltered} />)
    expect(container.querySelector('[data-card="finding"]')).toBeTruthy()
    expect(container.querySelector('.animate-slide-up')).toBeNull()
  })

  it('筛选评估者：发现出现，并计入它的条目数', () => {
    const s = useConversationStore.getState()
    s.ingest(finding(1))
    s.ingest(finding(2))
    s.ingest(reasoningMsg(3, 'evaluator', '评估结论：坐实'))
    render(<TimelineThread {...unfiltered} agents={['evaluator']} />)
    expect(document.querySelectorAll('[data-card="finding"]').length).toBe(2)
    expect(screen.getByText(/评估结论：坐实/)).toBeTruthy()
    expect(screen.getByRole('button', { name: /^评估\s*3，工具调用\s*0$/ })).toBeTruthy()
  })

  it('筛选别的智能体：发现不出现（它不是任何一段工作的共同上下文）', () => {
    const s = useConversationStore.getState()
    s.ingest(finding(1))
    s.ingest(toolCallMsg(2, 'executor'))
    render(<TimelineThread {...unfiltered} agents={['executor']} />)
    expect(document.querySelector('[data-card="finding"]')).toBeNull()
    // 执行者的这一轮照旧在；评估者的条目数里含这条发现
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
    expect(screen.getByRole('button', { name: /^评估\s*1，工具调用\s*0$/ })).toBeTruthy()
  })

  it('真正的系统节点在各个筛选下都留着', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '开始'))
    s.ingest({
      ...base,
      seq: 2,
      role: 'assistant',
      kind: 'event',
      metadata: { kind: 'task_status', status: 'completed' } as TimelineEventMeta,
    })
    s.ingest(toolCallMsg(3, 'monitor'))
    render(<TimelineThread {...unfiltered} agents={['monitor']} />)
    expect(screen.getByText('开始')).toBeTruthy()
    expect(screen.getByText('任务完成')).toBeTruthy()
  })
})

describe('按智能体筛选', () => {
  beforeEach(() => useConversationStore.getState().reset())
  const group = () => screen.getByRole('search', { name: /^按智能体筛选/ })

  it('每个出现过的智能体一枚身份胶囊，带条目数和工具调用数（在途的轮都算）', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '开始'))
    s.ingest(reasoningMsg(2, 'planner', '规划'))
    s.ingest(reasoningMsg(3, 'executor', '执行 A'))
    s.ingest(toolCallMsg(4, 'executor'))
    render(<TimelineThread {...unfiltered} />)
    expect(within(group()).getByRole('button', { name: /^规划\s*1，工具调用\s*0$/ })).toBeTruthy()
    expect(within(group()).getByRole('button', { name: /^执行\s*2，工具调用\s*1$/ })).toBeTruthy()
    // 两个数的含义写在区域名里，读屏进到这一区就听得到
    expect(group().getAttribute('aria-label')).toContain('依次是条目数和工具调用数')
  })

  it('没有智能体参与过的对话（纯聊天）不放筛选', () => {
    useConversationStore.getState().ingest(userMsg(1, '你好'))
    render(<TimelineThread {...unfiltered} />)
    expect(screen.queryByRole('search', { name: '按智能体筛选' })).toBeNull()
  })

  it('只选一个：其余智能体的卡与流式道隐去，人的指令留着', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '开始'))
    s.ingest(reasoningMsg(2, 'planner', '规划者的结论'))
    s.ingest(reasoningMsg(3, 'executor', '执行 A'))
    s.appendReasoningDelta('评估中…', 'evaluator')
    render(<TimelineThread {...unfiltered} agents={['executor']} />)
    expect(screen.getByText('开始')).toBeTruthy()
    expect(screen.getByText(/执行 A/)).toBeTruthy()
    expect(screen.queryByText(/规划者的结论/)).toBeNull()
    expect(document.querySelector('[data-live-node]')).toBeNull()
    expect(within(group()).getByRole('button', { name: /^执行\s*1，工具调用\s*0$/ }).getAttribute('aria-pressed')).toBe('true')
    expect(within(group()).getByRole('button', { name: /^规划\s*1，工具调用\s*0$/ }).getAttribute('aria-pressed')).toBe('false')
  })

  it('多选：选中的都留着，没选的隐去', () => {
    const s = useConversationStore.getState()
    s.ingest(userMsg(1, '开始'))
    s.ingest(reasoningMsg(2, 'planner', '规划者的结论'))
    s.ingest(reasoningMsg(3, 'executor', '执行 A'))
    s.ingest(reasoningMsg(4, 'monitor', '监察结论'))
    render(<TimelineThread {...unfiltered} agents={['executor', 'planner']} />)
    expect(screen.getByText(/规划者的结论/)).toBeTruthy()
    expect(screen.getByText(/执行 A/)).toBeTruthy()
    expect(screen.queryByText(/监察结论/)).toBeNull()
  })

  it('带着这个对话里没有的智能体进来时，筛选不生效', () => {
    useConversationStore.getState().ingest(reasoningMsg(1, 'executor', '执行 A'))
    render(<TimelineThread {...unfiltered} agents={['monitor']} />)
    expect(screen.getByText(/执行 A/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: '清空筛选' })).toBeNull()
  })

  it('点胶囊是多选的取舍：点没选中的是加上，点选中的是去掉；清空筛选交回空', async () => {
    const user = userEvent.setup()
    const onAgentsChange = vi.fn()
    const s = useConversationStore.getState()
    s.ingest(reasoningMsg(1, 'executor', '执行 A'))
    s.ingest(reasoningMsg(2, 'planner', '规划'))
    const { rerender } = render(<TimelineThread agents={[]} onAgentsChange={onAgentsChange} />)
    await user.click(within(group()).getByRole('button', { name: /^执行\s*1，工具调用\s*0$/ }))
    expect(onAgentsChange).toHaveBeenLastCalledWith(['executor'])

    rerender(<TimelineThread agents={['executor']} onAgentsChange={onAgentsChange} />)
    await user.click(within(group()).getByRole('button', { name: /^规划\s*1，工具调用\s*0$/ }))
    expect(onAgentsChange).toHaveBeenLastCalledWith(['executor', 'planner'])

    rerender(<TimelineThread agents={['executor', 'planner']} onAgentsChange={onAgentsChange} />)
    await user.click(within(group()).getByRole('button', { name: /^执行\s*1，工具调用\s*0$/ }))
    expect(onAgentsChange).toHaveBeenLastCalledWith(['planner'])

    rerender(<TimelineThread agents={['planner']} onAgentsChange={onAgentsChange} />)
    await user.click(screen.getByRole('button', { name: '清空筛选' }))
    expect(onAgentsChange).toHaveBeenLastCalledWith([])
  })
})
