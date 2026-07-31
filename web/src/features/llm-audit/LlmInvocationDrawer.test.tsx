import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { LlmInvocationDrawer } from './LlmInvocationDrawer'
import type { LLMInvocationDetail } from '@/api/types'

function makeDetail(overrides: Partial<LLMInvocationDetail> = {}): LLMInvocationDetail {
  return {
    id: 1,
    request_id: 'req-abc-123',
    hunter_id: null,
    task_id: 'task-1',
    provider: 'openai',
    model: 'gpt-5',
    in_tokens: 100,
    out_tokens: 50,
    cached_tokens: 0,
    latency_ms: 2000,
    ttft_ms: 0,
    is_stream: false,
    finish_reason: 'stop',
    error_message: '',
    role: 'orchestrator',
    created_at: '2026-01-01T00:00:00Z',
    tool_names: [],
    text_preview: '',
    messages: [],
    result: null,
    ...overrides,
  }
}

describe('LlmInvocationDrawer', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  it('loading=true 时显示加载中', () => {
    render(<LlmInvocationDrawer open detail={null} loading error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('加载中…')).toBeTruthy()
  })

  it('error 存在时显示错误信息', () => {
    render(<LlmInvocationDrawer open detail={null} loading={false} error="网络错误" onOpenChange={vi.fn()} />)
    expect(screen.getByText(/网络错误/)).toBeTruthy()
  })

  it('detail 存在时渲染 role/model/元信息 KV', () => {
    const detail = makeDetail({ role: 'exploitation', model: 'gpt-5-mini', provider: 'anthropic' })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    // 头部角色显示中文标签（agentLabel('exploitation') === '利用'），不再是原始英文 id。
    expect(screen.getByText('利用')).toBeTruthy()
    expect(screen.getByText('gpt-5-mini')).toBeTruthy()
    expect(screen.getByText(/anthropic/)).toBeTruthy()
    expect(screen.getByText('req-abc-123')).toBeTruthy()
  })

  it('error_message 存在时显示错误区块与失败标签', () => {
    const detail = makeDetail({ error_message: '连接超时' })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('失败')).toBeTruthy()
    expect(screen.getByText('错误')).toBeTruthy()
    expect(screen.getByText('连接超时')).toBeTruthy()
  })

  it('无 error_message 时不显示失败标签，显示 finish_reason', () => {
    const detail = makeDetail({ error_message: '', finish_reason: 'stop' })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.queryByText('失败')).toBeNull()
    // finish_reason 同时出现在头部徽章与元信息 KV 网格，共两处。
    expect(screen.getAllByText('stop').length).toBeGreaterThanOrEqual(2)
  })

  it('本次输入只显示最后一条消息（增量），不展示完整历史', () => {
    const detail = makeDetail({
      messages: [
        { role: 'system', content: '你是一个助手' },
        { role: 'user', content: '第一条' },
        { role: 'user', content: '最新这条' },
      ],
    })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('最新这条')).toBeTruthy()
    expect(screen.queryByText('第一条')).toBeNull()
    expect(screen.queryByText('你是一个助手')).toBeNull()
    // 增量提示：上下文共 3 条
    expect(screen.getByText(/共 3 条/)).toBeTruthy()
  })

  it('messages 只有一条时不显示增量提示', () => {
    const detail = makeDetail({ messages: [{ role: 'user', content: '唯一一条' }] })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('唯一一条')).toBeTruthy()
    expect(screen.queryByText(/增量/)).toBeNull()
  })

  it('reasoning_content 字段变体：result.reasoning_content 渲染思考过程区块', () => {
    const detail = makeDetail({ result: { content: '答案', reasoning_content: '先分析再回答' } })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('思考过程')).toBeTruthy()
    expect(screen.getByText('先分析再回答')).toBeTruthy()
  })

  it('reasoning_content 字段变体：result.extra["reasoning-content"] 渲染思考过程区块', () => {
    const detail = makeDetail({ result: { content: '答案', extra: { 'reasoning-content': '备用字段思考' } } })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('思考过程')).toBeTruthy()
    expect(screen.getByText('备用字段思考')).toBeTruthy()
  })

  it('result 无 reasoning 字段时不渲染思考过程区块', () => {
    const detail = makeDetail({ result: { content: '仅正文' } })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.queryByText('思考过程')).toBeNull()
  })

  it('result 为字符串时渲染为返回结果正文', () => {
    const detail = makeDetail({ result: '纯文本返回结果' })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('纯文本返回结果')).toBeTruthy()
  })

  it('result.tool_calls 存在时渲染工具调用卡片', () => {
    const detail = makeDetail({
      result: {
        tool_calls: [{ id: 'c1', function: { name: 'run_command', arguments: '{"cmd":"ls"}' } }],
      },
    })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('run_command')).toBeTruthy()
    expect(screen.getByText(/工具调用/)).toBeTruthy()
  })

  it('result 为 null 时显示无返回内容', () => {
    const detail = makeDetail({ result: null })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('无返回内容')).toBeTruthy()
  })

  it('复制按钮：点击复制 request_id', async () => {
    const writeText = navigator.clipboard.writeText
    const detail = makeDetail({ request_id: 'req-xyz' })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    // request_id 旁的复制按钮是 compact（仅图标，title="复制"），与其他 section 的文字按钮区分。
    const copyBtn = screen.getByTitle('复制')
    fireEvent.click(copyBtn)
    // copy() 内部 await clipboard.writeText 后才 setState；等按钮态切换（title 变为「已复制」），避免 act() 警告。
    expect(await screen.findByTitle('已复制')).toBeTruthy()
    expect(writeText).toHaveBeenCalledWith('req-xyz')
  })

  it('复制按钮：思考过程区块复制 reasoning 内容', async () => {
    const writeText = navigator.clipboard.writeText
    const detail = makeDetail({ result: { content: '答案', reasoning_content: '思考内容 A' } })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    const section = screen.getByText('思考过程').closest('h3')
    const copyBtn = section?.querySelector('button')
    expect(copyBtn).toBeTruthy()
    fireEvent.click(copyBtn!)
    expect(writeText).toHaveBeenCalledWith('思考内容 A')
    expect(await screen.findByText('已复制')).toBeTruthy()
  })

  it('无输入消息且无 messages 时显示占位', () => {
    const detail = makeDetail({ messages: null })
    render(<LlmInvocationDrawer open detail={detail} loading={false} error="" onOpenChange={vi.fn()} />)
    expect(screen.getByText('无输入消息')).toBeTruthy()
  })
})
