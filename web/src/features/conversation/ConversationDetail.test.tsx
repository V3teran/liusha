import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { ConversationDetail } from './ConversationDetail'
import { useConversationStore } from '@/stores/conversation'
import * as apiClient from '@/api/client'
import * as sseHook from '@/hooks/useEventStream'

vi.mock('@/api/client', () => ({
  listMessages: vi.fn(),
  getConversationUsage: vi.fn(),
  abortScan: vi.fn(),
  startChat: vi.fn(),
  followUp: vi.fn(),
  listScenarios: vi.fn(),
}))

vi.mock('@/hooks/useEventStream', () => ({
  openEventStream: vi.fn(() => ({ close: vi.fn() })),
}))

const mkUsage = (over: Partial<ReturnType<typeof baseUsage>> = {}) => ({ ...baseUsage(), ...over })
function baseUsage() {
  return {
    conversation_id: 'c1',
    owner_id: 'o1',
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
    // Composer 常驻 ScenarioPicker，会调用 listScenarios。
    vi.mocked(apiClient.listScenarios).mockResolvedValue([])
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('无 convId（主动下发 manual）显示空状态引导文案', async () => {
    render(<ConversationDetail source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('发起一次渗透扫描')).toBeTruthy()
  })

  it('无 convId（被动代理 auto）显示对应空状态文案', async () => {
    render(<ConversationDetail source="auto" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('选择一批流量查看分析')).toBeTruthy()
  })

  it('有 convId 时拉取历史消息并订阅 SSE', async () => {
    vi.mocked(apiClient.listMessages).mockResolvedValue([
      { Seq: 1, ID: 'm1', ConversationID: 'c1', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' },
    ])
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    expect(sseHook.openEventStream).toHaveBeenCalled()
  })

  it('历史加载失败显示错误重试卡', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValue(new Error('network'))
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('加载对话失败')).toBeTruthy()
    expect(screen.getByText('重试')).toBeTruthy()
  })

  it('点击重试重新加载历史', async () => {
    vi.mocked(apiClient.listMessages).mockRejectedValueOnce(new Error('network'))
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    const retryBtn = await screen.findByText('重试')
    vi.mocked(apiClient.listMessages).mockResolvedValueOnce([])
    retryBtn.click()
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledTimes(2))
  })

  it('扫描运行中（usage.running=true）显示"agent 工作中"与停止按钮', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('agent 工作中…')).toBeTruthy()
    // 停止按钮同时出现在顶部状态栏 + Composer 区域（scanning 态下两处都渲染）。
    await waitFor(() => expect(screen.getAllByText('停止扫描').length).toBeGreaterThan(0))
  })

  it('点击停止扫描调用 abortScan', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    vi.mocked(apiClient.abortScan).mockResolvedValue(undefined)
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    const stopBtns = await waitFor(() => {
      const btns = screen.getAllByText('停止扫描')
      expect(btns.length).toBeGreaterThan(0)
      return btns
    })
    stopBtns[0].click()
    await waitFor(() => expect(apiClient.abortScan).toHaveBeenCalledWith('c1'))
  })

  it('运行态翻转触发 onRunningChanged 回调', async () => {
    const onRunningChanged = vi.fn()
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: false }))
    const { rerender } = render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={onRunningChanged} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalled())
    onRunningChanged.mockClear()

    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(mkUsage({ running: true, status: 'active' }))
    rerender(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={onRunningChanged} />)
    await waitFor(() => expect(screen.queryByText('agent 工作中…')).toBeTruthy())
  })

  it('切换 convId 时重置 store 并重新拉取', async () => {
    const { rerender } = render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c1'))
    rerender(<ConversationDetail convId="c2" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    await waitFor(() => expect(apiClient.listMessages).toHaveBeenCalledWith('c2'))
  })

  it('token 用量存在时显示 tokens/耗时 metrics', async () => {
    vi.mocked(apiClient.getConversationUsage).mockResolvedValue(
      mkUsage({ tokens: { in: 100, out: 50, cached: 0, total: 150 }, duration_ms: 5000 }),
    )
    render(<ConversationDetail convId="c1" source="manual" onStarted={vi.fn()} onRunningChanged={vi.fn()} />)
    expect(await screen.findByText('tokens')).toBeTruthy()
    expect(screen.getByText('耗时')).toBeTruthy()
  })
})
