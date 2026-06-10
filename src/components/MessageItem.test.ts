import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MessageItem from './MessageItem.vue'
import type { Message } from '../api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function mk(over: Partial<Message>): Message {
  return { Seq: 1, Role: 'tool', Kind: 'event', Metadata: null, ...base, ...over } as Message
}

describe('MessageItem 分发', () => {
  it('user message → UserBubble', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Role: 'user', Kind: 'message', Content: '扫这个' }) } })
    expect(w.find('[data-card="user"]').exists()).toBe(true)
  })
  it('tool_call → ToolCallCard 含工具名', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: 'ls', Result: '', DurationMs: 0, Err: '' } }) } })
    expect(w.find('[data-card="tool-call"]').text()).toContain('run_command')
  })
  it('tool_result+Err → 错误态', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_result', ToolName: 'x', Args: '', Result: '', DurationMs: 5, Err: 'boom' } }) } })
    expect(w.find('[data-card="tool-result"][data-error="true"]').exists()).toBe(true)
  })
  it('write_finding 结果 → FindingCard', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_result', ToolName: 'write_finding', Args: '', Result: 'SQLi at /login', DurationMs: 3, Err: '' } }) } })
    expect(w.find('[data-card="finding"]').exists()).toBe(true)
  })
})
