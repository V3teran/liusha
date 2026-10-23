import { describe, expect, it } from 'vitest'
import { inFlightToolCalls } from './threadRows'
import type { Message } from '../api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function toolMsg(seq: number, kind: 'tool_call' | 'tool_result', agent: string, tool: string): Message {
  return {
    Seq: seq,
    Role: 'assistant',
    Kind: 'event',
    ...base,
    Metadata: { kind, agent_name: agent, tool_name: tool },
  } as Message
}

describe('inFlightToolCalls', () => {
  it('配对完成的调用不在执行中集合', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'run_command'),
      toolMsg(2, 'tool_result', 'executor', 'run_command'),
    ]
    expect(inFlightToolCalls(msgs).size).toBe(0)
  })

  it('悬挂 call（已发未见配对 result）在执行中集合', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'run_command'),
      toolMsg(2, 'tool_result', 'executor', 'run_command'),
      toolMsg(3, 'tool_call', 'executor', 'run_command'),
    ]
    const running = inFlightToolCalls(msgs)
    expect(running.size).toBe(1)
    expect(running.has(3)).toBe(true)
  })

  it('并发同名工具交错到达按 FIFO 配对（call call result → 首个消耗，末个执行中）', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'run_command'),
      toolMsg(2, 'tool_call', 'executor', 'run_command'),
      toolMsg(3, 'tool_result', 'executor', 'run_command'),
    ]
    const running = inFlightToolCalls(msgs)
    expect(running.size).toBe(1)
    expect(running.has(2)).toBe(true)
  })

  it('不同 agent / 工具互不串配', () => {
    const msgs = [
      toolMsg(1, 'tool_call', 'executor', 'run_command'),
      toolMsg(2, 'tool_result', 'evaluator', 'run_command'), // 异 agent result 不消耗
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
    if (callID) m.Metadata!.call_id = callID
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
    expect(pairs.matched.get(2)?.Seq).toBe(3) // c2 的 result（seq3）配给 call2——FIFO 会错配给 call1
    expect(pairs.matched.get(1)?.Seq).toBe(4) // c1 的 result（seq4）配给 call1
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
    expect(pairs.matched.get(1)?.Seq).toBe(3)
    expect(inFlightToolCalls(msgs).has(2)).toBe(true)
  })
})
