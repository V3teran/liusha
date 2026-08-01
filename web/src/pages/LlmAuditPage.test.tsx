import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { LlmAuditPage } from './LlmAuditPage'
import {
  getLLMInvocationDetail,
  getLLMInvocationFacets,
  getLLMInvocationStat,
  listLLMInvocations,
} from '@/api/client'
import type { LLMInvocationDetail, LLMInvocationStat, LLMInvocationSummary } from '@/api/types'

vi.mock('@/api/client', () => ({
  listLLMInvocations: vi.fn(),
  getLLMInvocationStat: vi.fn(),
  getLLMInvocationFacets: vi.fn(),
  getLLMInvocationDetail: vi.fn(),
}))

vi.mock('@/components/OwnerPicker', () => ({
  OwnerPicker: (props: { value?: string; onChange: (id: string) => void }) => (
    <button type="button" onClick={() => props.onChange('owner-1')}>
      pick:{props.value}
    </button>
  ),
}))

const mockedListLLMInvocations = listLLMInvocations as unknown as ReturnType<typeof vi.fn>
const mockedGetLLMInvocationStat = getLLMInvocationStat as unknown as ReturnType<typeof vi.fn>
const mockedGetLLMInvocationFacets = getLLMInvocationFacets as unknown as ReturnType<typeof vi.fn>
const mockedGetLLMInvocationDetail = getLLMInvocationDetail as unknown as ReturnType<typeof vi.fn>

function makeSummary(overrides: Partial<LLMInvocationSummary> = {}): LLMInvocationSummary {
  return {
    id: 1,
    request_id: 'req-1',
    hunter_id: null,
    task_id: 'owner-1',
    provider: 'openai',
    model: 'gpt-5',
    in_tokens: 100,
    out_tokens: 50,
    cached_tokens: 0,
    latency_ms: 1500,
    ttft_ms: 0,
    is_stream: false,
    finish_reason: 'stop',
    error_message: '',
    role: 'orchestrator',
    created_at: '2026-01-01T00:00:00Z',
    tool_names: [],
    text_preview: '一些文本预览',
    ...overrides,
  }
}

function makeStat(overrides: Partial<LLMInvocationStat> = {}): LLMInvocationStat {
  return {
    task_id: 'owner-1',
    calls: 5,
    in_tokens: 1000,
    out_tokens: 500,
    cached_tokens: 0,
    latency_ms: 5000,
    ...overrides,
  }
}

function setupDefaultMocks() {
  mockedListLLMInvocations.mockResolvedValue({
    task_id: 'owner-1',
    total: 1,
    next_after: 1,
    has_more: false,
    items: [makeSummary()],
  })
  mockedGetLLMInvocationStat.mockResolvedValue(makeStat())
  mockedGetLLMInvocationFacets.mockResolvedValue({ task_id: 'owner-1', roles: ['orchestrator', 'exploitation'], models: ['gpt-5'] })
}

// React Query 需要 Provider；筛选/分页状态走 URL 需要 Router。retry:false 避免测试里
// mock 拒绝时反复重试拖慢用例。
function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/llm-audit']}>
        <LlmAuditPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('LlmAuditPage', () => {
  beforeEach(() => {
    mockedListLLMInvocations.mockReset()
    mockedGetLLMInvocationStat.mockReset()
    mockedGetLLMInvocationFacets.mockReset()
    mockedGetLLMInvocationDetail.mockReset()
  })

  it('未选择 owner 时显示占位提示', () => {
    renderPage()
    expect(screen.getByText('请选择一个对话查看 LLM 调用审计')).toBeTruthy()
  })

  it('选择 owner 后统计 chip 渲染 getLLMInvocationStat 数据', async () => {
    const user = userEvent.setup()
    setupDefaultMocks()
    renderPage()

    await user.click(screen.getByText(/pick:/))

    await waitFor(() => expect(mockedGetLLMInvocationStat).toHaveBeenCalled())
    expect(await screen.findByText('5')).toBeTruthy() // 调用数
  })

  it('表格行渲染 listLLMInvocations items', async () => {
    const user = userEvent.setup()
    setupDefaultMocks()
    mockedListLLMInvocations.mockResolvedValue({
      task_id: 'owner-1',
      total: 1,
      next_after: 1,
      has_more: false,
      items: [makeSummary({ role: 'exploitation', model: 'gpt-5-mini' })],
    })
    renderPage()
    await user.click(screen.getByText(/pick:/))

    // "exploitation" 同时出现在角色筛选下拉的 <option> 和表格行标签中，用行内定位区分。
    const row = await screen.findByLabelText(/查看调用详情/)
    expect(within(row).getByText('利用')).toBeTruthy() // agentLabel('exploitation')
    expect(within(row).getByText('gpt-5-mini')).toBeTruthy()
  })

  it('切换 role 筛选触发重新加载并重置分页', async () => {
    const user = userEvent.setup()
    setupDefaultMocks()
    renderPage()
    await user.click(screen.getByText(/pick:/))
    await screen.findByText('exploitation') // role 下拉候选（原始英文 id）
    mockedListLLMInvocations.mockClear()

    const roleSelect = screen.getByText('全部角色').closest('select')!
    await user.selectOptions(roleSelect, 'orchestrator')

    await waitFor(() =>
      expect(mockedListLLMInvocations).toHaveBeenCalledWith(
        'owner-1',
        0,
        100,
        expect.objectContaining({ role: 'orchestrator' }),
      ),
    )
  })

  it('切换仅错误 checkbox 触发重新加载', async () => {
    const user = userEvent.setup()
    setupDefaultMocks()
    renderPage()
    await user.click(screen.getByText(/pick:/))
    await screen.findByText('exploitation')
    mockedListLLMInvocations.mockClear()

    const checkbox = screen.getByRole('checkbox')
    await user.click(checkbox)

    await waitFor(() =>
      expect(mockedListLLMInvocations).toHaveBeenCalledWith(
        'owner-1',
        0,
        100,
        expect.objectContaining({ onlyErr: true }),
      ),
    )
  })

  it('下一页使用 next_after 作为游标，上一页弹栈回退', async () => {
    const user = userEvent.setup()
    mockedGetLLMInvocationStat.mockResolvedValue(makeStat())
    mockedGetLLMInvocationFacets.mockResolvedValue({ task_id: 'owner-1', roles: [], models: [] })
    mockedListLLMInvocations
      .mockResolvedValueOnce({ task_id: 'owner-1', total: 1, next_after: 10, has_more: true, items: [makeSummary({ id: 1 })] })
      .mockResolvedValueOnce({ task_id: 'owner-1', total: 1, next_after: 20, has_more: false, items: [makeSummary({ id: 2, role: 'exploitation' })] })

    renderPage()
    await user.click(screen.getByText(/pick:/))
    await screen.findByLabelText(/查看调用详情/)

    const nextBtn = screen.getByRole('button', { name: '下一页' })
    await user.click(nextBtn)

    await waitFor(() =>
      expect(mockedListLLMInvocations).toHaveBeenLastCalledWith('owner-1', 10, 100, expect.anything()),
    )
    expect(await screen.findByText('利用')).toBeTruthy() // agentLabel('exploitation')

    const prevBtn = screen.getByRole('button', { name: '上一页' })
    await user.click(prevBtn)
    await waitFor(() =>
      expect(mockedListLLMInvocations).toHaveBeenLastCalledWith('owner-1', 0, 100, expect.anything()),
    )
  })

  it('下一页按钮在 hasMore=false 时 disabled', async () => {
    const user = userEvent.setup()
    setupDefaultMocks() // has_more: false
    renderPage()
    await user.click(screen.getByText(/pick:/))
    await screen.findByLabelText(/查看调用详情/)

    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
  })

  it('点击行打开详情抽屉并调用 getLLMInvocationDetail', async () => {
    const user = userEvent.setup()
    setupDefaultMocks()
    const detail: LLMInvocationDetail = { ...makeSummary(), messages: [], result: null }
    mockedGetLLMInvocationDetail.mockResolvedValue(detail)
    renderPage()
    await user.click(screen.getByText(/pick:/))

    const row = await screen.findByLabelText(/查看调用详情/)
    await user.click(row)

    await waitFor(() => expect(mockedGetLLMInvocationDetail).toHaveBeenCalledWith('owner-1', 1))
    // 抽屉打开后元信息区块可见
    expect(await screen.findByText('元信息')).toBeTruthy()
  })
})
