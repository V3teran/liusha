import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChatThread } from './ChatThread'
import { useConversationStore } from '@/stores/conversation'

describe('ChatThread', () => {
  beforeEach(() => useConversationStore.getState().reset())

  it('会话消息独立成卡，普通工具调用折叠进步组（默认收起）', () => {
    const s = useConversationStore.getState()
    s.ingest({
      Seq: 1,
      ID: 'm1',
      ConversationID: 'c',
      Role: 'user',
      Kind: 'message',
      Content: 'hi',
      Metadata: null,
      CreatedAt: '',
    })
    s.ingest({
      Seq: 2,
      ID: 'm2',
      ConversationID: 'c',
      Role: 'tool',
      Kind: 'event',
      Content: '',
      Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' } as never,
      CreatedAt: '',
    })
    const { container } = render(<ChatThread />)
    // 折叠默认收起：只有 user 卡可见，工具卡藏在折叠组里。
    expect(container.querySelectorAll('[data-card]')).toHaveLength(1)
    expect(screen.getByText(/1 次工具调用/)).toBeTruthy()
  })

  it('展开折叠组后工具卡可见', async () => {
    const s = useConversationStore.getState()
    s.ingest({
      Seq: 1,
      ID: 'm1',
      ConversationID: 'c',
      Role: 'user',
      Kind: 'message',
      Content: 'hi',
      Metadata: null,
      CreatedAt: '',
    })
    s.ingest({
      Seq: 2,
      ID: 'm2',
      ConversationID: 'c',
      Role: 'tool',
      Kind: 'event',
      Content: '',
      Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' } as never,
      CreatedAt: '',
    })
    const { container } = render(<ChatThread />)
    await userEvent.click(screen.getByText(/1 次工具调用/))
    expect(container.querySelectorAll('[data-card]')).toHaveLength(2) // user + 展开的工具卡
  })

  // Bug2 回归：流式活动气泡须与落定推理卡样式一致——带头像 + 按 agent 取色/标签，
  // 而不是裸卡、中性灰、无标签（修复前的样子）。
  it('流式推理气泡带头像 + agent 配色 + agent 标签', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('正在分析响应…', 'traffic-analysis')
    const { container } = render(<ChatThread />)
    // (1) 头像：流式气泡套了头像（与落定的 reasoning 卡一致）
    const avatar = container.querySelector('img[alt="agent"]')
    expect(avatar).toBeTruthy()
    // (2) 配色：推理卡 accent 是 traffic-analysis 的靛蓝 #818cf8，不是空名回退的中性灰
    const card = container.querySelector('[data-card="reasoning"]')
    expect(card).toBeTruthy()
    expect(card?.getAttribute('style')).toContain('#818cf8')
    // (3) 标签：显示中文 agent 标签「流量分析」
    expect(card?.textContent).toContain('流量分析')
  })

  it('流式气泡无 agent 名时不崩（回退中性态）', () => {
    const s = useConversationStore.getState()
    s.appendReasoningDelta('思考中…') // 不带 agent 名
    const { container } = render(<ChatThread />)
    expect(container.querySelector('[data-card="reasoning"]')).toBeTruthy()
    expect(container.querySelector('img[alt="agent"]')).toBeTruthy()
  })
})
