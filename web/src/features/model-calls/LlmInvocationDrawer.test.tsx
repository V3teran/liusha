import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { LlmInvocationDrawer } from './LlmInvocationDrawer'
import type { LLMInvocationDetail } from '@/api/types'

function makeDetail(overrides: Partial<LLMInvocationDetail> = {}): LLMInvocationDetail {
  return {
    id: 1,
    agent_run_id: null,
    task_id: 'task-1',
    conversation_id: null,
    assignment_id: null,
    provider: 'openai',
    model: 'gpt-5',
    in_tokens: 100,
    out_tokens: 50,
    cached_tokens: 0,
    latency_ms: 2000,
    finish_reason: 'stop',
    error_message: '',
    error_kind: '',
    role: 'planner',
    created_at: '2026-01-01T00:00:00Z',
    messages: [],
    result: null,
    ...overrides,
  }
}

// 抽屉元信息含任务/对话跳转 Link，需 Router 上下文。
function renderDrawer(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

describe('LlmInvocationDrawer', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  it('可滚动的长文本区块都可键盘聚焦（键盘用户能滚动阅读）', () => {
    const detail = makeDetail({
      error_message: '上游超时',
      messages: [{ role: 'user', content: '输入正文' }],
      result: { content: '返回正文' },
    } as Partial<LLMInvocationDetail>)
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    const blocks = document.querySelectorAll('pre')
    expect(blocks.length).toBeGreaterThan(0)
    blocks.forEach((pre) => expect(pre.getAttribute('tabindex')).toBe('0'))
  })

  it('loading=true 时显示加载中', () => {
    renderDrawer(<LlmInvocationDrawer open detail={null} loading error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('加载中…')).toBeTruthy()
  })

  it('error 存在时显示错误信息', () => {
    renderDrawer(<LlmInvocationDrawer open detail={null} loading={false} error="网络错误" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText(/网络错误/)).toBeTruthy()
  })

  it('detail 存在时渲染 role/model/元信息 KV', () => {
    const detail = makeDetail({ role: 'executor', model: 'gpt-5-mini', provider: 'anthropic' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    // 头部角色显示中文标签（agentLabel('executor') === '执行'），不再是原始英文 id。
    expect(screen.getByText('执行')).toBeTruthy()
    expect(screen.getByText('gpt-5-mini')).toBeTruthy()
    expect(screen.getByText(/anthropic/)).toBeTruthy()
    expect(screen.getByText('#1')).toBeTruthy()
  })

  it('跳转区排在元信息之前：回到产出上下文是最先用到的出口', () => {
    renderDrawer(<LlmInvocationDrawer open detail={makeDetail()} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    const jump = screen.getByRole('heading', { name: '跳转' })
    const meta = screen.getByRole('heading', { name: '元信息' })
    expect(jump.compareDocumentPosition(meta) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('error_message 存在时显示错误区块与失败标签', () => {
    const detail = makeDetail({ error_message: '连接超时' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('失败')).toBeTruthy()
    expect(screen.getByText('错误')).toBeTruthy()
    expect(screen.getByText('连接超时')).toBeTruthy()
  })

  it('无 error_message 时不显示失败标签，显示 finish_reason', () => {
    const detail = makeDetail({ error_message: '', finish_reason: 'stop' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.queryByText('失败')).toBeNull()
    // finish_reason 显示在头部徽章（元信息 KV 网格已不再重复展示）。
    expect(screen.getAllByText('stop').length).toBeGreaterThanOrEqual(1)
  })

  it('本次输入只显示最后一条消息（增量），不展示完整历史', () => {
    const detail = makeDetail({
      messages: [
        { role: 'system', content: '你是一个助手' },
        { role: 'user', content: '第一条' },
        { role: 'user', content: '最新这条' },
      ],
    })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('最新这条')).toBeTruthy()
    expect(screen.queryByText('第一条')).toBeNull()
    expect(screen.queryByText('你是一个助手')).toBeNull()
    // 增量提示：上下文共 3 条
    expect(screen.getByText(/共 3 条/)).toBeTruthy()
  })

  it('messages 只有一条时不显示增量提示', () => {
    const detail = makeDetail({ messages: [{ role: 'user', content: '唯一一条' }] })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('唯一一条')).toBeTruthy()
    expect(screen.queryByText(/增量/)).toBeNull()
  })

  it('result 无 reasoning 字段时不渲染思考过程区块', () => {
    const detail = makeDetail({ result: { content: '仅正文' } })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.queryByText('思考过程')).toBeNull()
  })

  it('result 为字符串时渲染为返回结果正文', () => {
    const detail = makeDetail({ result: '纯文本返回结果' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('纯文本返回结果')).toBeTruthy()
  })

  it('result.tool_calls 存在时渲染工具调用卡片', () => {
    const detail = makeDetail({
      result: {
        tool_calls: [{ id: 'c1', name: 'command_run', arguments: { cmd: 'ls' } }],
      },
    })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('command_run')).toBeTruthy()
    expect(screen.getByText(/工具调用/)).toBeTruthy()
  })

  it('result 为 null 时显示无返回内容', () => {
    const detail = makeDetail({ result: null })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('无返回内容')).toBeTruthy()
  })

  it('复制按钮：点击复制调用编号', async () => {
    const writeText = navigator.clipboard.writeText
    const detail = makeDetail({ id: 42 })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    // 编号旁的复制按钮是图标态（CopyButton 以 aria-label 表达复制/已复制态）。
    const copyBtn = screen.getAllByRole('button', { name: '复制' })[0]
    fireEvent.click(copyBtn)
    // copy() 内部 await clipboard.writeText 后才 setState；等按钮态切换（aria-label 变为「已复制」），避免 act() 警告。
    expect(await screen.findByRole('button', { name: '已复制' })).toBeTruthy()
    expect(screen.getByText('#42')).toBeTruthy()
    expect(writeText).toHaveBeenCalledWith('42')
  })

  it('复制按钮：思考过程区块复制 reasoning 内容', async () => {
    const writeText = navigator.clipboard.writeText
    const detail = makeDetail({ result: { content: '答案', thinking: { text: '思考内容 A' } } })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    const section = screen.getByText('思考过程').closest('h3')
    const copyBtn = section?.querySelector('button')
    expect(copyBtn).toBeTruthy()
    fireEvent.click(copyBtn!)
    expect(writeText).toHaveBeenCalledWith('思考内容 A')
    expect(await screen.findByRole('button', { name: '已复制' })).toBeTruthy()
  })

  it('无输入消息且无 messages 时显示占位', () => {
    const detail = makeDetail({ messages: null })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByText('无输入消息')).toBeTruthy()
  })
})

describe('LlmInvocationDrawer 跳转按钮', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  it('属于任务的调用：任务、对话、探索图，对话 tab 按来源定', () => {
    const detail = makeDetail({ task_id: 't-1', conversation_id: 'c-1', source: 'proxy' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByRole('link', { name: '任务' }).getAttribute('href')).toBe('/tasks?task=t-1&selected=t-1')
    expect(screen.getByRole('link', { name: '对话' }).getAttribute('href')).toBe('/conversations/proxy?conversation=c-1')
    expect(screen.getByRole('link', { name: '探索图' }).getAttribute('href')).toBe('/exploration-graph?task=t-1')
    // 发现属于任务而不是某次调用，这里不放
    expect(screen.queryByRole('link', { name: '发现' })).toBeNull()
  })

  it('source 缺省落 direct tab', () => {
    const detail = makeDetail({ task_id: 't-2', conversation_id: 'c-2' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByRole('link', { name: '对话' }).getAttribute('href')).toBe('/conversations/direct?conversation=c-2')
  })

  it('对话里直接发起的调用（没有任务）：只跳回所在对话', () => {
    const detail = makeDetail({ task_id: null, conversation_id: 'c-3', role: 'inspector' })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.getByRole('link', { name: '对话' }).getAttribute('href')).toBe('/conversations/direct?conversation=c-3')
    expect(screen.queryByRole('link', { name: '任务' })).toBeNull()
    expect(screen.queryByRole('link', { name: '探索图' })).toBeNull()
  })

  it('无 task/conversation 时不渲染按钮', () => {
    const detail = makeDetail({ task_id: null, conversation_id: null })
    renderDrawer(<LlmInvocationDrawer open detail={detail} loading={false} error="" onRetry={vi.fn()} onOpenChange={vi.fn()} />)
    expect(screen.queryByRole('heading', { name: '跳转' })).toBeNull()
  })
})
