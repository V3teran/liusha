import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render as rtlRender, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConversationDetail } from './ConversationDetail'
import { useConversationStore } from '@/stores/conversation'
import * as apiClient from '@/api/conversations'
import * as tasksClient from '@/api/tasks'
import * as sseHook from '@/hooks/useEventStream'
import { ApiError } from '@/api/client'
import type { Conversation, TaskSummary, TaskUsage } from '@/api/types'

vi.mock('@/api/conversations', () => ({
  listMessages: vi.fn(),
  getConversation: vi.fn(),
  startChat: vi.fn(),
  followUp: vi.fn(),
}))

vi.mock('@/api/tasks', () => ({
  controlTask: vi.fn(),
  getTaskDetail: vi.fn(),
  getTaskUsage: vi.fn(),
}))

vi.mock('@/hooks/useEventStream', () => ({
  openEventStream: vi.fn(() => ({ close: vi.fn() })),
}))

// 每个用例一个独立的 QueryClient：缓存不跨用例。rerender 沿用同一个 client。
function render(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (el: ReactElement) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>{el}</MemoryRouter>
    </QueryClientProvider>
  )
  const r = rtlRender(wrap(ui))
  return { ...r, rerender: (el: ReactElement) => r.rerender(wrap(el)) }
}

const conv = (over: Partial<Conversation> = {}): Conversation =>
  ({ id: 'c1', title: '测试', task_id: 'o1', created_at: '', updated_at: '', ...over }) as Conversation

const taskOf = (over: Partial<TaskSummary> = {}): TaskSummary =>
  ({ id: 'o1', status: 'completed', paused: false, ...over }) as TaskSummary

const usageOf = (over: Partial<TaskUsage> = {}): TaskUsage => ({
  tokens: { in: 0, out: 0, cached: 0 },
  llm_calls: 0,
  llm_latency_ms: 0,
  tool_calls: 0,
  tool_duration_ms: 0,
  elapsed_ms: 0,
  ...over,
})

function withTask(over: Partial<TaskSummary>) {
  vi.mocked(tasksClient.getTaskDetail).mockResolvedValue(taskOf(over))
}

describe('ConversationDetail', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(apiClient.listMessages).mockResolvedValue([])
    vi.mocked(apiClient.getConversation).mockResolvedValue(conv())
    vi.mocked(tasksClient.getTaskDetail).mockResolvedValue(taskOf())
    vi.mocked(tasksClient.getTaskUsage).mockResolvedValue(usageOf())
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('无 convId（人工下发 direct）显示空状态引导文案', async () => {
    render(<ConversationDetail source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('发起一次渗透测试任务')).toBeTruthy()
  })

  it('无 convId（代理捕获 proxy）显示「未选择代理批次」', async () => {
    render(<ConversationDetail source="proxy" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('未选择代理批次')).toBeTruthy()
  })

  it('有 convId 时拉取历史消息并订阅 SSE', async () => {
    vi.mocked(apiClient.listMessages).mockResolvedValue([
      { seq: 1, id: 'm1', conversation_id: 'c1', role: 'user', kind: 'message', content: 'hi', metadata: null, created_at: '' },
    ])
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    expect(sseHook.openEventStream).toHaveBeenCalled()
  })

  it('历史请求返回前组件已卸载：不得再建立 SSE 连接', async () => {
    let resolve!: (v: never[]) => void
    vi.mocked(apiClient.listMessages).mockReturnValue(new Promise((r) => (resolve = r)))
    const { unmount } = render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    unmount()
    resolve([])
    await new Promise((r) => setTimeout(r, 0))
    expect(sseHook.openEventStream).not.toHaveBeenCalled()
  })

  it('历史加载失败显示错误重试卡', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValue(new Error('network'))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('加载对话失败')).toBeTruthy()
    expect(screen.getByText('重试')).toBeTruthy()
  })

  it('点击重试重新加载历史', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValueOnce(new Error('network'))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    const retryBtn = await screen.findByText('重试')
    vi.mocked(apiClient.listMessages).mockResolvedValueOnce([])
    retryBtn.click()
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledTimes(2))
  })

  it('扫描运行中显示"智能体工作中"与 暂停/停止 控制组', async () => {
    withTask({ status: 'active' })
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('智能体工作中…')).toBeTruthy()
    expect(screen.getByRole('button', { name: '暂停任务' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '停止任务' })).toBeTruthy()
  })

  it('暂停中显示"已暂停"与 继续/停止 控制组', async () => {
    withTask({ status: 'active', paused: true })
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('已暂停（进度保留）')).toBeTruthy()
    expect(screen.getByRole('button', { name: '继续任务' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '停止任务' })).toBeTruthy()
    expect(screen.queryByText('智能体工作中…')).toBeNull()
  })

  it('点击暂停任务直接调用任务控制端点（可逆，无确认弹层）', async () => {
    withTask({ status: 'active' })
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    ;(await screen.findByRole('button', { name: '暂停任务' })).click()
    await waitFor(() => expect(tasksClient.controlTask).toHaveBeenCalledWith('o1', 'pause'))
  })

  it('暂停中点切换钮直接调用任务控制端点（resume）', async () => {
    withTask({ status: 'active', paused: true })
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    ;(await screen.findByRole('button', { name: '继续任务' })).click()
    await waitFor(() => expect(tasksClient.controlTask).toHaveBeenCalledWith('o1', 'resume'))
  })

  it('点击停止任务经确认弹层后调用任务控制端点（terminate）', async () => {
    withTask({ status: 'active' })
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    ;(await screen.findByRole('button', { name: '停止任务' })).click()
    expect(tasksClient.controlTask).not.toHaveBeenCalled()
    ;(await screen.findByRole('button', { name: '确认停止' })).click()
    await waitFor(() => expect(tasksClient.controlTask).toHaveBeenCalledWith('o1', 'terminate'))
  })

  it('停止任务在确认弹层取消时不调用端点', async () => {
    withTask({ status: 'active' })
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    ;(await screen.findByRole('button', { name: '停止任务' })).click()
    ;(await screen.findByRole('button', { name: '取消' })).click()
    await waitFor(() => expect(screen.queryByRole('button', { name: '确认停止' })).toBeNull())
    expect(tasksClient.controlTask).not.toHaveBeenCalled()
  })

  it('运行态翻转触发 onRunningChanged 回调（新消息落定后按任务刷新状态）', async () => {
    const onRunningChanged = vi.fn()
    withTask({ status: 'completed' })
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={onRunningChanged} />)
    await waitFor(() => expect(tasksClient.getTaskDetail).toHaveBeenCalledWith('o1'))
    onRunningChanged.mockClear()

    withTask({ status: 'active' })
    act(() => useConversationStore.getState().ingest({ seq: 9, id: 'm9', conversation_id: 'c1', role: 'assistant', kind: 'event', content: '', metadata: null, created_at: '' }))
    expect(await screen.findByText('智能体工作中…', {}, { timeout: 3000 })).toBeTruthy()
    expect(onRunningChanged).toHaveBeenCalled()
  })

  // 跳转在对话列表行的「⋯」菜单里，顶栏不重复
  it('顶栏不放跳转', async () => {
    withTask({ status: 'completed', conversation_id: 'c1', finding_count: 2, source: 'direct' })
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(tasksClient.getTaskDetail).toHaveBeenCalledWith('o1'))
    expect(screen.queryByRole('link', { name: '漏洞' })).toBeNull()
  })

  it('纯聊天对话（没有任务）不显示控制组，也不取任务', async () => {
    vi.mocked(apiClient.getConversation).mockResolvedValue(conv({ task_id: '' }))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('对话')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '停止任务' })).toBeNull()
    expect(screen.queryByRole('link', { name: '任务' })).toBeNull()
    expect(tasksClient.getTaskDetail).not.toHaveBeenCalled()
    expect(tasksClient.getTaskUsage).not.toHaveBeenCalled()
  })

  it('纯聊天转为执行后（对话关联上任务），新消息落定即接上任务状态与控制组', async () => {
    vi.mocked(apiClient.getConversation).mockResolvedValue(conv({ task_id: '' }))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await screen.findByText('对话')

    vi.mocked(apiClient.getConversation).mockResolvedValue(conv({ task_id: 'o1' }))
    withTask({ status: 'active' })
    act(() => useConversationStore.getState().ingest({ seq: 3, id: 'm3', conversation_id: 'c1', role: 'assistant', kind: 'event', content: '任务已下发', metadata: null, created_at: '' }))
    expect(await screen.findByRole('button', { name: '停止任务' }, { timeout: 3000 })).toBeTruthy()
    expect(tasksClient.getTaskDetail).toHaveBeenCalledWith('o1')
  })

  it('切换 convId 时重置 store 并重新拉取', async () => {
    const { rerender } = render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    rerender(<ConversationDetail convId="c2" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c2'))
  })

  it('token 用量存在时显示 tokens/耗时 metrics', async () => {
    vi.mocked(tasksClient.getTaskUsage).mockResolvedValue(usageOf({ tokens: { in: 100, out: 50, cached: 0 }, elapsed_ms: 5000 }))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('tokens')).toBeTruthy()
    expect(screen.getByText('耗时')).toBeTruthy()
  })

  it('对话不存在（404，如已删除对话的旧深链）：提示不存在且不提供必然失败的重试', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValue(new ApiError('GET', 404, '资源不存在'))
    render(<ConversationDetail convId="gone" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('对话不存在')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
    expect(sseHook.openEventStream).not.toHaveBeenCalled()
  })

  it('其他加载失败（5xx / 网络）：保留重试入口', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValue(new ApiError('GET', 503, '服务不可用'))
    render(<ConversationDetail convId="c1" source="direct" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('加载对话失败')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy()
  })
})
