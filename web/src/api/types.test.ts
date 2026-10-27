import { describe, it, expect } from 'vitest'
import { isEventMessage, parseScanEvent } from './types'
import type { Message } from './types'

describe('SSE 帧解析（Go 大写键）', () => {
  const frame: Message = {
    Seq: 42,
    ID: 'm1',
    ConversationID: 'c1',
    Role: 'tool',
    Kind: 'event',
    Content: 'done',
    // ScanEvent 是后端手工 map 序列化的小写 snake_case 键（types.ts 契约）。
    Metadata: {
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
    CreatedAt: '2026-06-10T00:00:00Z',
  }

  it('识别 event 消息', () => {
    expect(isEventMessage(frame)).toBe(true)
  })

  it('识别 message 消息', () => {
    const msg: Message = { ...frame, Kind: 'message', Metadata: null }
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
    const m: Message = { ...frame, Kind: 'message', Metadata: null }
    expect(parseScanEvent(m)).toBeNull()
  })

  it('event 消息但 metadata 为 null 返回 null', () => {
    const m: Message = { ...frame, Kind: 'event', Metadata: null }
    expect(parseScanEvent(m)).toBeNull()
  })
})
