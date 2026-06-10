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
    Metadata: {
      Kind: 'tool_result',
      ToolName: 'run_command',
      Args: '',
      Result: 'ok',
      DurationMs: 12,
      Err: '',
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

  it('解析 ScanEvent metadata', () => {
    const ev = parseScanEvent(frame)
    expect(ev).not.toBeNull()
    expect(ev?.ToolName).toBe('run_command')
    expect(ev?.Kind).toBe('tool_result')
    expect(ev?.Result).toBe('ok')
    expect(ev?.DurationMs).toBe(12)
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
