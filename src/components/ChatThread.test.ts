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
    expect(toggle.text()).toContain('1 次工具调用')
  })

  it('展开折叠组后工具卡可见', async () => {
    const s = useConversationStore()
    s.ingest({ Seq: 1, ID: 'm1', ConversationID: 'c', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' })
    s.ingest({ Seq: 2, ID: 'm2', ConversationID: 'c', Role: 'tool', Kind: 'event', Content: '', Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' }, CreatedAt: '' })
    const w = mount(ChatThread)
    await w.find('.st-toggle').trigger('click')
    expect(w.findAll('[data-card]')).toHaveLength(2) // user + 展开的工具卡
  })

  // Bug2 回归：流式活动气泡须与落定推理卡样式一致——带头像 + 按 agent 取色/标签，
  // 而不是裸卡、中性灰、无标签（修复前的样子）。
  it('流式推理气泡带头像 + agent 配色 + agent 标签', () => {
    const s = useConversationStore()
    s.appendReasoningDelta('正在分析响应…', 'traffic-analysis')
    const w = mount(ChatThread)
    // (1) 头像：流式气泡套了 avatar-slot（与落定的 reasoning 卡一致）
    const avatar = w.find('.avatar-slot img.avatar')
    expect(avatar.exists()).toBe(true)
    expect(avatar.attributes('alt')).toBe('agent')
    // (2) 配色：推理卡 accent 是 traffic-analysis 的蓝 #1890ff，不是空名回退的中性灰 #8c8c8c
    const card = w.find('[data-card="reasoning"]')
    expect(card.exists()).toBe(true)
    expect(card.attributes('style')).toContain('#1890ff')
    // (3) 标签：显示中文 agent 标签「流量分析」
    expect(card.text()).toContain('流量分析')
  })

  it('流式气泡无 agent 名时不崩（回退中性态）', () => {
    const s = useConversationStore()
    s.appendReasoningDelta('思考中…') // 不带 agent 名
    const w = mount(ChatThread)
    expect(w.find('[data-card="reasoning"]').exists()).toBe(true)
    expect(w.find('.avatar-slot img.avatar').exists()).toBe(true)
  })
})
