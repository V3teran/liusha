import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { MessageItem } from './MessageItem'
import type { Message } from '@/api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function mk(over: Partial<Message>): Message {
  return { Seq: 1, Role: 'tool', Kind: 'event', Metadata: null, ...base, ...over } as Message
}

describe('MessageItem 分发', () => {
  it('user message → UserBubble', () => {
    const { container } = render(
      <MessageItem msg={mk({ Role: 'user', Kind: 'message', Content: '扫这个' })} />,
    )
    expect(container.querySelector('[data-card="user"]')).toBeTruthy()
  })

  it('tool_call → ToolCallCard 含工具名', () => {
    const { container } = render(
      <MessageItem
        msg={mk({
          Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: 'ls', Result: '', DurationMs: 0, Err: '' } as never,
        })}
      />,
    )
    expect(container.querySelector('[data-card="tool-call"]')?.textContent).toContain('run_command')
  })

  it('tool_result+Err → 错误态', () => {
    const { container } = render(
      <MessageItem
        msg={mk({
          Metadata: { Kind: 'tool_result', ToolName: 'x', Args: '', Result: '', DurationMs: 5, Err: 'boom' } as never,
        })}
      />,
    )
    expect(container.querySelector('[data-card="tool-result"][data-error="true"]')).toBeTruthy()
  })

  it('write_finding(tool_call 含 summary) → FindingCard', () => {
    const { container } = render(
      <MessageItem
        msg={mk({
          Role: 'assistant',
          Metadata: {
            Kind: 'tool_call',
            ToolName: 'write_finding',
            Args: '{"summary":"SQLi at /login","severity":"high"}',
            Result: '',
            DurationMs: 0,
            Err: '',
          } as never,
        })}
      />,
    )
    expect(container.querySelector('[data-card="finding"]')).toBeTruthy()
  })

  it('write_finding 缺 summary → 隐藏（不渲染 INFO 无标题）', () => {
    const { container } = render(
      <MessageItem
        msg={mk({
          Role: 'assistant',
          Metadata: {
            Kind: 'tool_call',
            ToolName: 'write_finding',
            Args: '{"cwe_id":"CWE-89"}',
            Result: '',
            DurationMs: 0,
            Err: '',
          } as never,
        })}
      />,
    )
    expect(container.querySelector('[data-card="finding"]')).toBeFalsy()
  })

  it('write_finding tool_result → 隐藏（仅 {id} 无展示价值）', () => {
    const { container } = render(
      <MessageItem
        msg={mk({
          Metadata: {
            Kind: 'tool_result',
            ToolName: 'write_finding',
            Args: '',
            Result: '{"id":"x"}',
            DurationMs: 3,
            Err: '',
          } as never,
        })}
      />,
    )
    expect(container.firstElementChild).toBeNull()
  })
})
