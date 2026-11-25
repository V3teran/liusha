import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { LlmAuditPage } from './LlmAuditPage'
import { getLLMInvocationDetail, searchLLMInvocationStat, searchLLMInvocations } from '@/api/llmaudit'
import type { LLMInvocationDetail, LLMInvocationSummary } from '@/api/types'

// 页面为「全局搜索」形态：不按会话 / 任务圈定，挂载即 searchLLMInvocations 拉全量；
// 统计 chip 走服务端聚合端点 searchLLMInvocationStat（整个筛选结果集，与翻页无关）。
vi.mock('@/api/llmaudit', () => ({
  searchLLMInvocations: vi.fn(),
  searchLLMInvocationStat: vi.fn(),
  searchLLMInvocationFacets: vi.fn(async () => ({ roles: ['executor', 'planner'], models: ['deepseek-chat', 'gpt-5'] })),
  getLLMInvocationDetail: vi.fn(),
}))

// runtime 现读（LLM 看门狗同源）：mock 避免测试内真实 fetch。
vi.mock('@/api/settings', () => ({
  getRuntimeSettings: vi.fn().mockResolvedValue({
    step_tool_timeout_seconds: 1800,
    step_llm_timeout_seconds: 300,
    run_tail_bytes: 8192,
  }),
}))

const mockedSearch = searchLLMInvocations as unknown as ReturnType<typeof vi.fn>
const mockedStat = searchLLMInvocationStat as unknown as ReturnType<typeof vi.fn>
const mockedGetDetail = getLLMInvocationDetail as unknown as ReturnType<typeof vi.fn>

function makeSummary(overrides: Partial<LLMInvocationSummary> = {}): LLMInvocationSummary {
  return {
    id: 1,
    agent_run_id: null,
    task_id: 'owner-1',
    conversation_id: 'conv-1',
    assignment_id: null,
    provider: 'openai',
    model: 'gpt-5',
    in_tokens: 100,
    out_tokens: 50,
    cached_tokens: 0,
    latency_ms: 1500,
    finish_reason: 'stop',
    error_message: '',
    error_kind: '',
    role: 'planner',
    created_at: '2026-01-01T00:00:00Z',
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
function renderPage(entry = '/llm-audit') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[entry]}>
        <LlmAuditPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function LocationProbe() {
  return <output data-testid="location">{useLocation().search}</output>
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
    // 角色在表格徽章与下拉选项里都显示中文名（同一份身份登记），不显示英文角色名
    expect(screen.getAllByText('规划').length).toBeGreaterThan(0)
    expect(screen.queryByText('planner')).toBeNull()
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

    const box = screen.getByPlaceholderText('搜索任务、对话、批次 ID')
    await userEvent.type(box, 'conv-1')
    await waitFor(() => expect(mockedSearch).toHaveBeenCalledWith('conv-1', 1, 50, expect.anything()))
  })

  // 「想看最新数据时不知道点哪里」：词已经生效时，搜索框上的「刷新」按当前的词重取一遍。
  it('词已生效时点搜索框的「刷新」重取最新数据', async () => {
    setupDefaultMocks()
    renderPage()
    await screen.findAllByText('gpt-5')
    mockedSearch.mockClear()

    await userEvent.click(screen.getByRole('button', { name: '刷新' }))
    await waitFor(() => expect(mockedSearch).toHaveBeenCalledWith('', 1, 50, expect.anything()))
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

  it('加载失败显示错误态，重试重新请求', async () => {
    mockedSearch.mockRejectedValue(new Error('500'))
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('加载失败')
    expect(alert).toHaveTextContent('500')
    const calls = mockedSearch.mock.calls.length
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(mockedSearch.mock.calls.length).toBeGreaterThan(calls))
  })

  it('点击行打开详情抽屉并按 id 拉取详情', async () => {
    setupDefaultMocks()
    renderPage()
    await screen.findAllByText('gpt-5')

    await userEvent.click(screen.getByRole('row', { name: '查看调用详情 #1' }))

    await waitFor(() => expect(mockedGetDetail).toHaveBeenCalledWith(1))
    // 抽屉渲染详情的调用编号。
    expect(await screen.findByText('#1')).toBeTruthy()
    // 打开的调用写在 URL：跳到任务 / 对话再后退时还原抽屉
    expect(screen.getByTestId('location').textContent).toBe('?selected=1')
  })

  it('带 ?selected= 进页直接打开这条调用的抽屉', async () => {
    setupDefaultMocks()
    renderPage('/llm-audit?selected=1')
    await waitFor(() => expect(mockedGetDetail).toHaveBeenCalledWith(1))
    expect(await screen.findByText('#1')).toBeTruthy()
  })
})

describe('LlmAuditPage 角色 / 模型选项', () => {
  it('选项来自服务端（整个筛选结果集），不是当前页的行：当前页只有 gpt-5 也能选其它模型', async () => {
    setupDefaultMocks()
    renderPage()
    const model = (await screen.findByRole('combobox', { name: /模型/ })) as HTMLSelectElement
    await waitFor(() => expect(within(model).getByRole('option', { name: 'deepseek-chat' })).toBeTruthy())
    const role = screen.getByRole('combobox', { name: /角色/ }) as HTMLSelectElement
    expect(within(role).getByRole('option', { name: '执行' })).toBeTruthy()
  })
})
