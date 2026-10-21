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
