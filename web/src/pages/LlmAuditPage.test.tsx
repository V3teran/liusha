import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { LlmAuditPage } from './LlmAuditPage'
import { getLLMInvocationDetail, searchLLMInvocationStat, searchLLMInvocations } from '@/api/client'
import type { LLMInvocationDetail, LLMInvocationSummary } from '@/api/types'

// 页面为「全局搜索」形态：不按会话（owner）圈定，挂载即 searchLLMInvocations 拉全量；
// 统计 chip 走服务端聚合端点 searchLLMInvocationStat（整个筛选结果集，与翻页无关）。
vi.mock('@/api/client', () => ({
  searchLLMInvocations: vi.fn(),
  searchLLMInvocationStat: vi.fn(),
  getLLMInvocationDetail: vi.fn(),
}))

const mockedSearch = searchLLMInvocations as unknown as ReturnType<typeof vi.fn>
const mockedStat = searchLLMInvocationStat as unknown as ReturnType<typeof vi.fn>
const mockedGetDetail = getLLMInvocationDetail as unknown as ReturnType<typeof vi.fn>

function makeSummary(overrides: Partial<LLMInvocationSummary> = {}): LLMInvocationSummary {
  return {
    id: 1,
    request_id: 'req-1',
    agent_run_id: null,
    task_id: 'owner-1',
    conversation_id: 'conv-1',
    provider: 'openai',
    model: 'gpt-5',
    in_tokens: 100,
    out_tokens: 50,
    cached_tokens: 0,
    latency_ms: 1500,
    finish_reason: 'stop',
    error_message: '',
    role: 'planner',
    created_at: '2026-01-01T00:00:00Z',
    tool_names: [],
    text_preview: '一些文本预览',
    ...overrides,
  }
}

function makeDetail(overrides: Partial<LLMInvocationSummary> = {}): LLMInvocationDetail {
  return { ...makeSummary(overrides), messages: [], result: { content: '答案' } }
}

function setupDefaultMocks() {
  mockedSearch.mockResolvedValue({
    total: 1,
    page: 1,
    size: 50,
    items: [makeSummary()],
  })
  mockedStat.mockResolvedValue({ calls: 197, in_tokens: 509245, out_tokens: 40121, cached_tokens: 148800, latency_ms: 1 })
  mockedGetDetail.mockResolvedValue(makeDetail())
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
    mockedSearch.mockReset()
    mockedStat.mockReset()
    mockedGetDetail.mockReset()
  })

  it('挂载即全局拉取（空搜索）并渲染表格行', async () => {
    setupDefaultMocks()
    renderPage()

    await waitFor(() =>
      expect(mockedSearch).toHaveBeenCalledWith(
        '',
        1,
        50,
        expect.objectContaining({ role: '', model: '' }),
      ),
    )
    expect((await screen.findAllByText('gpt-5')).length).toBeGreaterThan(0)
    expect(screen.getAllByText('planner').length).toBeGreaterThan(0)
  })

  it('统计 chip 来自服务端聚合（searchLLMInvocationStat）', async () => {
    setupDefaultMocks()
    renderPage()

    expect(await screen.findByText('调用')).toBeTruthy()
    expect(mockedStat).toHaveBeenCalledWith('', expect.objectContaining({ role: '', model: '' }))
    // 聚合值来自 stat 接口（calls=197），不是当页行数（1 行）
    expect(await screen.findByText('197')).toBeTruthy()
    expect(screen.getByText('输入')).toBeTruthy()
    expect(screen.getByText('输出')).toBeTruthy()
  })

  it('搜索词防抖后进入全局搜索参数', async () => {
    setupDefaultMocks()
    renderPage()
    await screen.findAllByText('gpt-5')

    const box = screen.getByPlaceholderText('搜索对话、批次 ID...')
    await userEvent.type(box, 'conv-1')
    await waitFor(() => expect(mockedSearch).toHaveBeenCalledWith('conv-1', 1, 50, expect.anything()))
  })

  it('切换 role 筛选触发重新加载并重置分页', async () => {
    setupDefaultMocks()
    renderPage()
    await screen.findAllByText('gpt-5')

    const roleSelect = screen.getByDisplayValue('全部角色')
    await userEvent.selectOptions(roleSelect, 'planner')

    await waitFor(() =>
      expect(mockedSearch).toHaveBeenCalledWith(
        '',
        1,
        50,
        expect.objectContaining({ role: 'planner' }),
      ),
    )
  })

  it('空结果显示空态文案', async () => {
    mockedSearch.mockResolvedValue({ total: 0, page: 1, size: 50, items: [] })
    renderPage()

    expect(await screen.findByText('暂无 LLM 调用记录')).toBeTruthy()
  })

  it('加载失败显示错误态', async () => {
    mockedSearch.mockRejectedValue(new Error('500'))
    renderPage()

    expect(await screen.findByText(/加载失败：500/)).toBeTruthy()
  })

  it('点击行打开详情抽屉并按 id 拉取详情', async () => {
    setupDefaultMocks()
    renderPage()
    await screen.findAllByText('gpt-5')

    await userEvent.click(screen.getByRole('button', { name: '查看调用详情 req-1' }))

    await waitFor(() => expect(mockedGetDetail).toHaveBeenCalledWith(1))
    // 抽屉渲染详情的 request_id。
    expect(await screen.findByText('req-1')).toBeTruthy()
  })
})
