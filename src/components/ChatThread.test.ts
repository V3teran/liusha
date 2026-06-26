import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ChatThread from './ChatThread.vue'
import { useConversationStore } from '../stores/conversation'

describe('ChatThread', () => {
  beforeEach(() => setActivePinia(createPinia()))
  it('对话消息独立成卡，普通工具调用折叠进步组（默认收起）', () => {
    const s = useConversationStore()
    s.ingest({ Seq: 1, ID: 'm1', ConversationID: 'c', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' })
    s.ingest({ Seq: 2, ID: 'm2', ConversationID: 'c', Role: 'tool', Kind: 'event', Content: '', Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' }, CreatedAt: '' })
    const w = mount(ChatThread)
    // 折叠默认收起：只有 user 卡可见，工具卡藏在折叠组里。
    expect(w.findAll('[data-card]')).toHaveLength(1)
    const toggle = w.find('.st-toggle')
    expect(toggle.exists()).toBe(true)
    expect(toggle.text()).toContain('1 个工具调用')
  })

  it('展开折叠组后工具卡可见', async () => {
    const s = useConversationStore()
    s.ingest({ Seq: 1, ID: 'm1', ConversationID: 'c', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' })
    s.ingest({ Seq: 2, ID: 'm2', ConversationID: 'c', Role: 'tool', Kind: 'event', Content: '', Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' }, CreatedAt: '' })
    const w = mount(ChatThread)
    await w.find('.st-toggle').trigger('click')
    expect(w.findAll('[data-card]')).toHaveLength(2) // user + 展开的工具卡
  })
})
