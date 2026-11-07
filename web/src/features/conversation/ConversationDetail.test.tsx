import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ConversationDetail } from './ConversationDetail'
import { useConversationStore } from '@/stores/conversation'
import * as apiClient from '@/api/conversations'
import * as tasksClient from '@/api/tasks'
import * as sseHook from '@/hooks/useEventStream'

vi.mock('@/api/conversations', () => ({
  listMessages: vi.fn(),
  getConversationUsage: vi.fn(),
  abortTask: vi.fn(),
  startChat: vi.fn(),
  followUp: vi.fn(),
}))

vi.mock('@/api/tasks', () => ({
  controlTask: vi.fn(),
}))

vi.mock('@/hooks/useEventStream', () => ({
  openEventStream: vi.fn(() => ({ close: vi.fn() })),
}))

const mkUsage = (over: Partial<ReturnType<typeof baseUsage>> = {}) => ({ ...baseUsage(), ...over })
function baseUsage() {
  return {
    conversation_id: 'c1',
    task_id: 'o1',
    tokens: { in: 0, out: 0, cached: 0, total: 0 },
    llm_latency_ms: 0,
    tool_duration_ms: 0,
    duration_ms: 0,
    work_ms: 0,
    llm_calls: 0,
    tool_calls: 0,
    running: false,
    status: 'completed',
  }
}

describe('ConversationDetail', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(apiClient.listMessages).mockResolvedValue([])
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage())
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('无 convId（自主下发 self）显示空状态引导文案', async () => {
    render(<MemoryRouter><ConversationDetail source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('发起一次渗透测试任务')).toBeTruthy()
  })

  it('无 convId（代理模式 proxy）显示对应空状态文案', async () => {
    render(<MemoryRouter><ConversationDetail source="proxy" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('选择一批流量查看分析')).toBeTruthy()
  })

  it('有 convId 时拉取历史消息并订阅 SSE', async () => {
    vi.mocked(apiClient.listMessages).mockResolvedValue([
      { seq: 1, id: 'm1', conversation_id: 'c1', role: 'user', kind: 'message', content: 'hi', metadata: null, created_at: '' },
    ])
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    expect(sseHook.openEventStream).toHaveBeenCalled()
  })

  it('历史加载失败显示错误重试卡', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValue(new Error('network'))
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('加载对话失败')).toBeTruthy()
    expect(screen.getByText('重试')).toBeTruthy()
  })

  it('点击重试重新加载历史', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValueOnce(new Error('network'))
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    const retryBtn = await screen.findByText('重试')
    vi.mocked(apiClient.listMessages).mockResolvedValueOnce([])
    retryBtn.click()
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledTimes(2))
  })

  it('扫描运行中显示"agent 工作中"与 暂停/停止 控制组', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('agent 工作中…')).toBeTruthy()
    expect(screen.getByRole('button', { name: '暂停扫描' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '停止扫描' })).toBeTruthy()
  })

  it('暂停中显示"已暂停"与 继续/停止 控制组', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active', paused: true }))
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('已暂停（进度保留）')).toBeTruthy()
    expect(screen.getByRole('button', { name: '继续扫描' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '停止扫描' })).toBeTruthy()
    expect(screen.queryByText('agent 工作中…')).toBeNull()
  })

  it('点击暂停扫描调用任务控制端点（pause）', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    ;(await screen.findByRole('button', { name: '暂停扫描' })).click()
    await waitFor(() => expect(tasksClient.controlTask).toHaveBeenCalledWith('o1', 'pause'))
  })

  it('点击停止扫描（确认后）调用任务控制端点（terminate）', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    vi.mocked(tasksClient.controlTask).mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    ;(await screen.findByRole('button', { name: '停止扫描' })).click()
    await waitFor(() => expect(tasksClient.controlTask).toHaveBeenCalledWith('o1', 'terminate'))
  })

  it('运行态翻转触发 onRunningChanged 回调', async () => {
    const onRunningChanged = vi.fn()
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: false, status: 'completed' }))
    const { rerender } = render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={onRunningChanged} /></MemoryRouter>)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalled())
    onRunningChanged.mockClear()

    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    rerender(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={onRunningChanged} /></MemoryRouter>)
    await waitFor(() => expect(screen.queryByText('agent 工作中…')).toBeTruthy())
  })

  it('切换 convId 时重置 store 并重新拉取', async () => {
    const { rerender } = render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    rerender(<MemoryRouter><ConversationDetail convId="c2" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c2'))
  })

  it('token 用量存在时显示 tokens/耗时 metrics', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(
      mkUsage({ tokens: { in: 100, out: 50, cached: 0, total: 150 }, duration_ms: 5000 }),
    )
    render(<MemoryRouter><ConversationDetail convId="c1" source="self" onStarted={vi.fn()} onRunningChanged={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByText('tokens')).toBeTruthy()
    expect(screen.getByText('耗时')).toBeTruthy()
  })
})
