import { describe, expect, it } from 'vitest'
import { agentActivity, buildThreadRows, filterByAgent, inFlightToolCalls, roundKey } from './threadRows'
import type { Message } from '@/api/types'

const base = { conversation_id: 'c', created_at: '', content: '' }

function toolMsg(seq: number, kind: 'tool_call' | 'tool_result', agent: string, tool: string): Message {
  return {
    seq: seq,
    role: 'assistant',
    kind: 'event',
    ...base,
    metadata: { kind, agent_name: agent, tool_name: tool },
  } as Message
}

describe('inFlightToolCalls', () => {
  it('配对完成的调用不在执行中集合', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'command_run'),
      toolMsg(2, 'tool_result', 'executor', 'command_run'),
    ]
    expect(inFlightToolCalls(msgs).size).toBe(0)
  })

  it('悬挂 call（已发未见配对 result）在执行中集合', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'command_run'),
      toolMsg(2, 'tool_result', 'executor', 'command_run'),
      toolMsg(3, 'tool_call', 'executor', 'command_run'),
    ]
    const running = inFlightToolCalls(msgs)
    expect(running.size).toBe(1)
    expect(running.has(3)).toBe(true)
  })

  it('并发同名工具交错到达按 FIFO 配对（call call result → 首个消耗，末个执行中）', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'command_run'),
      toolMsg(2, 'tool_call', 'executor', 'command_run'),
      toolMsg(3, 'tool_result', 'executor', 'command_run'),
    ]
    const running = inFlightToolCalls(msgs)
    expect(running.size).toBe(1)
    expect(running.has(2)).toBe(true)
  })

  it('不同 agent / 工具互不串配', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'command_run'),
      toolMsg(2, 'tool_result', 'evaluator', 'command_run'), // 异 agent result 不消耗
      toolMsg(3, 'tool_result', 'executor', 'http_request'), // 异工具 result 不消耗
    ]
    const running = inFlightToolCalls(msgs)
    expect(running.size).toBe(1)
    expect(running.has(1)).toBe(true)
  })
})

// call_id 关联配对：并发同名工具乱序完成（call1 call2 result2 result1）时
// FIFO 会把 result2 配给 call1——事件内联身份后按 ID 精确配对。
describe('call_id 配对', () => {
  function toolMsgWithID(
    seq: number,
    kind: 'tool_call' | 'tool_result',
    agent: string,
    tool: string,
    callID?: string,
  ): Message {
    const m = toolMsg(seq, kind, agent, tool)
    if (callID) m.metadata!.call_id = callID
    return m
  }

  it('乱序完成按 call_id 精确配对（FIFO 会错配的场景）', async () => {
    const { pairToolResults } = await import('./threadRows')
    const msgs = [
      toolMsgWithID(1, 'tool_call', 'executor', 'http_request', 'c1'),
      toolMsgWithID(2, 'tool_call', 'executor', 'http_request', 'c2'),
      toolMsgWithID(3, 'tool_result', 'executor', 'http_request', 'c2'), // B 先完成
      toolMsgWithID(4, 'tool_result', 'executor', 'http_request', 'c1'),
    ]
    const pairs = pairToolResults(msgs)
    expect(pairs.matched.get(2)?.seq).toBe(3) // c2 的 result（seq3）配给 call2——FIFO 会错配给 call1
    expect(pairs.matched.get(1)?.seq).toBe(4) // c1 的 result（seq4）配给 call1
    expect(pairs.orphanResults.length).toBe(0)
  })

  it('带 call_id 但 call 事件丢失的 result 是孤儿（不 FIFO 错拿他人 call）', async () => {
    const { pairToolResults } = await import('./threadRows')
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'http_request'), // 历史无 call_id
      toolMsgWithID(2, 'tool_result', 'executor', 'http_request', 'lost'),
    ]
    const pairs = pairToolResults(msgs)
    expect(pairs.orphanResults.length).toBe(1)
    expect(pairs.matched.size).toBe(0)
    expect(inFlightToolCalls(msgs).size).toBe(1) // 旧 call 仍未完成
  })

  it('无 call_id 的历史事件仍按 FIFO 配对', async () => {
    const { pairToolResults } = await import('./threadRows')
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'http_request'),
      toolMsg(2, 'tool_call', 'executor', 'http_request'),
      toolMsg(3, 'tool_result', 'executor', 'http_request'),
    ]
    const pairs = pairToolResults(msgs)
    expect(pairs.matched.get(1)?.seq).toBe(3)
    expect(inFlightToolCalls(msgs).has(2)).toBe(true)
  })
})

describe('buildThreadRows：工具事件归到所属的轮', () => {
  const deps = { dayKey: () => '', dayLabel: () => '' }

  function toolMsgInRound(seq: number, kind: 'tool_call' | 'tool_result', agent: string, roundID: string): Message {
    const m = toolMsg(seq, kind, agent, 'command_run')
    m.metadata!.round_id = roundID
    return m
  }
  function reasoningInRound(seq: number, agent: string, roundID: string): Message {
    const m = { ...base, seq, role: 'assistant', kind: 'event', content: '结论', metadata: { kind: 'reasoning', agent_name: agent, round_id: roundID } }
    return m as Message
  }

  // 执行者并行跑两个动作：同一 agent 的工具事件交错到达，先结束的轮只领走自己那一轮的
  it('带轮次标识：并行的两轮各领各的工具事件', () => {
    const rows = buildThreadRows(
      [
        toolMsgInRound(1, 'tool_call', 'executor', 'a'),
        toolMsgInRound(2, 'tool_call', 'executor', 'b'),
        toolMsgInRound(3, 'tool_result', 'executor', 'a'),
        toolMsgInRound(4, 'tool_call', 'executor', 'b'),
        reasoningInRound(5, 'executor', 'a'),
        toolMsgInRound(6, 'tool_result', 'executor', 'b'),
        reasoningInRound(7, 'executor', 'b'),
      ],
      deps,
    )
    expect(rows.map((r) => r.kind)).toEqual(['msg', 'msg'])
    expect(toolsOf(rows[0]).map((t) => t.seq)).toEqual([1, 3])
    expect(toolsOf(rows[1]).map((t) => t.seq)).toEqual([2, 4, 6])
  })

  it('不带标识的事件归同一 agent 的下一条终稿', () => {
    const rows = buildThreadRows(
      [
        toolMsg(1, 'tool_call', 'planner', 'corpus_search'),
        toolMsg(2, 'tool_call', 'executor', 'http_request'), // 另一个 agent 的，不归规划者
        reasoningInRound(3, 'planner', 'p'),
      ],
      deps,
    )
    expect(toolsOf(rows[0]).map((t) => t.seq)).toEqual([1])
    expect(rows[1]).toMatchObject({ kind: 'round', agent: 'executor', tools: [expect.objectContaining({ seq: 2 })] })
  })

  it('终稿没到的轮排在线程最后，按首个事件的出现顺序', () => {
    const rows = buildThreadRows(
      [
        toolMsgInRound(1, 'tool_call', 'evaluator', 'v'),
        toolMsgInRound(2, 'tool_call', 'executor', 'e'),
        reasoningInRound(3, 'planner', 'p'),
      ],
      deps,
    )
    // 终稿行在前；两个没有终稿的轮按各自首个事件（seq 1、seq 2）的先后排在后面
    expect(rows[0]).toMatchObject({ kind: 'msg', msg: { seq: 3 } })
    expect(rows.slice(1).map((r) => (r.kind === 'round' ? r.agent : r.kind))).toEqual(['evaluator', 'executor'])
  })

  it('一轮从在途变成已出终稿，折叠组的标识不变（展开状态跟着它走）', () => {
    const inFlight = buildThreadRows([toolMsgInRound(1, 'tool_call', 'executor', 'a')], deps)
    const concluded = buildThreadRows([toolMsgInRound(1, 'tool_call', 'executor', 'a'), reasoningInRound(2, 'executor', 'a')], deps)
    expect(roundKey(toolsOf(inFlight[0]))).toBe(roundKey(toolsOf(concluded[0])))
  })
})

describe('按智能体筛选', () => {
  const deps = { dayKey: (iso: string) => iso, dayLabel: (iso: string) => iso }
  const at = (m: Message, day: string) => ({ ...m, created_at: day })
  const assistantMsg = (seq: number): Message =>
    ({ ...base, seq, role: 'assistant', kind: 'message', content: '回答', metadata: null }) as Message

  // 三天：第一天有人的指令、两个智能体各一轮与任务状态；第二天只有聊天回答；第三天只有人的指令
  function sample(): ThreadRowLike[] {
    return buildThreadRows(
      [
        at(userMsgLike(1, '第一条指令'), 'd1'),
        { ...base, seq: 2, role: 'assistant', kind: 'event', content: '规划', metadata: { kind: 'reasoning', agent_name: 'planner' }, created_at: 'd1' } as Message,
        { ...base, seq: 3, role: 'assistant', kind: 'event', content: '执行', metadata: { kind: 'reasoning', agent_name: 'executor' }, created_at: 'd1' } as Message,
        { ...base, seq: 4, role: 'assistant', kind: 'event', content: '', metadata: { kind: 'task_status', status: 'completed' }, created_at: 'd1' } as Message,
        at(assistantMsg(5), 'd2'),
        at(userMsgLike(6, '第三条指令'), 'd3'),
      ],
      deps,
    ) as ThreadRowLike[]
  }
  const outline = (rows: ThreadRowLike[]) =>
    rows.map((r) => (r.kind === 'divider' ? `divider:${r.label}` : r.kind === 'round' ? 'round' : `msg:${r.msg.metadata?.kind ?? r.msg.role}`))

  it('只留选中的那几个智能体的行；人的指令与系统节点留着做上下文', () => {
    expect(outline(filterByAgent(sample(), ['executor']))).toEqual([
      'divider:d1',
      'msg:user',
      'msg:reasoning',
      'msg:task_status',
      'divider:d3',
      'msg:user',
    ])
    expect(outline(filterByAgent(sample(), ['assistant']))).toEqual([
      'divider:d1',
      'msg:user',
      'msg:task_status',
      'divider:d2',
      'msg:assistant',
      'divider:d3',
      'msg:user',
    ])
  })

  it('多选：选中的都留着，没选的隐去', () => {
    expect(outline(filterByAgent(sample(), ['executor', 'assistant']))).toEqual([
      'divider:d1',
      'msg:user',
      'msg:reasoning',
      'msg:task_status',
      'divider:d2',
      'msg:assistant',
      'divider:d3',
      'msg:user',
    ])
  })

  it('一个都没选时原样返回', () => {
    const rows = sample()
    expect(filterByAgent(rows, [])).toBe(rows)
  })

  it('智能体清单：登记过的身份在前，其余按首现；条目数一轮算一条，工具调用数是它各轮的和', () => {
    const rows = buildThreadRows(
      [
        { ...base, seq: 1, role: 'assistant', kind: 'event', content: '', metadata: { kind: 'reasoning', agent_name: 'custom-agent' } } as Message,
        { ...base, seq: 2, role: 'assistant', kind: 'event', content: '', metadata: { kind: 'reasoning', agent_name: 'evaluator' } } as Message,
        { ...base, seq: 3, role: 'assistant', kind: 'event', content: '', metadata: { kind: 'reasoning', agent_name: 'planner' } } as Message,
        toolMsg(4, 'tool_call', 'evaluator', 'http_request'), // 没有终稿的轮也算一条
        at(assistantMsg(5), ''),
      ],
      deps,
    )
    expect(agentActivity(rows)).toEqual([
      { name: 'planner', count: 1, toolCalls: 0 },
      { name: 'evaluator', count: 2, toolCalls: 1 },
      { name: 'assistant', count: 1, toolCalls: 0 },
      { name: 'custom-agent', count: 1, toolCalls: 0 },
    ])
  })

  it('工具调用数：已出终稿的轮与还没有终稿的轮都算，结果事件不算', () => {
    const rows = buildThreadRows(
      [
        toolMsg(1, 'tool_call', 'executor', 'http_request'),
        toolMsg(2, 'tool_result', 'executor', 'http_request'),
        toolMsg(3, 'tool_call', 'executor', 'command_run'),
        { ...base, seq: 4, role: 'assistant', kind: 'event', content: '', metadata: { kind: 'reasoning', agent_name: 'executor' } } as Message,
        toolMsg(5, 'tool_call', 'executor', 'graph_read'),
      ],
      deps,
    )
    expect(agentActivity(rows)).toEqual([{ name: 'executor', count: 2, toolCalls: 3 }])
  })
})

type ThreadRowLike = ReturnType<typeof buildThreadRows>[number]
/** 一行名下的工具过程事件（msg 行的 tools，或 round 行的 tools）。 */
function toolsOf(row: ThreadRowLike): Message[] {
  if (row.kind === 'divider') return []
  return row.kind === 'round' ? row.tools : (row.tools ?? [])
}
function userMsgLike(seq: number, content: string): Message {
  return { ...base, seq, role: 'user', kind: 'message', metadata: null, content } as Message
}
