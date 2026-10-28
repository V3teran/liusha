import { describe, it, expect } from 'vitest'
import { isEventMessage, parseScanEvent } from './types'
import type { Message } from './types'

describe('SSE 帧解析（小写蛇形键（与后端 json tag 同口径））', () => {
  const frame: Message = {
    seq: 42,
    id: 'm1',
    conversation_id: 'c1',
    role: 'tool',
    kind: 'event',
    content: 'done',
    // ScanEvent 是后端手工 map 序列化的小写 snake_case 键（types.ts 契约）。
    metadata: {
      kind: 'tool_result',
      status: '',
      steps: 0,
      actions: 0,
      findings: 0,
      stop_why: '',
      reason: '',
      call_id: 'call-1',
      tool_name: 'run_command',
      args: '',
      result: 'ok',
      duration_ms: 12,
      err: '',
      summary: '',
      severity: '',
      host: '',
      target: '',
      text: '',
      thinking: '',
      agent_name: 'executor',
      in_tokens: 0,
      out_tokens: 0,
      latency_ms: 0,
    },
    created_at: '2026-06-10T00:00:00Z',
  }

  it('识别 event 消息', () => {
    expect(isEventMessage(frame)).toBe(true)
  })

  it('识别 message 消息', () => {
    const msg: Message = { ...frame, kind: 'message', metadata: null }
    expect(isEventMessage(msg)).toBe(false)
  })

  it('解析 ScanEvent metadata（snake_case 键）', () => {
    const ev = parseScanEvent(frame)
    expect(ev).not.toBeNull()
    expect(ev?.tool_name).toBe('run_command')
    expect(ev?.kind).toBe('tool_result')
    expect(ev?.result).toBe('ok')
    expect(ev?.duration_ms).toBe(12)
  })

  it('KindMessage 无 metadata 返回 null', () => {
    const m: Message = { ...frame, kind: 'message', metadata: null }
    expect(parseScanEvent(m)).toBeNull()
  })

  it('event 消息但 metadata 为 null 返回 null', () => {
    const m: Message = { ...frame, kind: 'event', metadata: null }
    expect(parseScanEvent(m)).toBeNull()
  })
})
