import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ChatThread from './ChatThread.vue'
import { useConversationStore } from '../stores/conversation'

describe('ChatThread', () => {
  beforeEach(() => setActivePinia(createPinia()))
  it('渲染 store 里每条消息为一个 MessageItem', () => {
    const s = useConversationStore()
    s.ingest({ Seq: 1, ID: 'm1', ConversationID: 'c', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' })
    s.ingest({ Seq: 2, ID: 'm2', ConversationID: 'c', Role: 'tool', Kind: 'event', Content: '', Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' }, CreatedAt: '' })
    const w = mount(ChatThread)
    expect(w.findAll('[data-card]')).toHaveLength(2)
  })
})
