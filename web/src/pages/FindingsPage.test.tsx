import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { FindingsPage } from './FindingsPage'
import { getFindingClassFacets, getFindingStat, getFindingTaskFacets, listFindings, updateFindingTriage } from '@/api/findings'
import type { FindingRow, FindingListResponse } from '@/api/types'
import { ConfirmHost } from '@/components/ui/confirm'
import { Toaster } from '@/components/ui/toast'

vi.mock('@/api/findings', () => ({
  listFindings: vi.fn(),
  updateFindingTriage: vi.fn(),
  getFindingStat: vi.fn(),
  getFinding: vi.fn(async (id: string) => ({
    id,
    seq: 9,
    severity: 'high',
    summary: '不在当前页的发现',
    host: 'b.example.com',
    class: 'other',
    class_name: '其他',
    cwe: '',
    source: 'direct',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
  })),
  getFindingClassFacets: vi.fn(async () => [
    { class: 'info_disclosure', name: '敏感信息泄露', count: 9 },
    { class: 'sql_injection', name: 'SQL 注入', count: 3 },
    { class: 'other', name: '其他', count: 1 },
  ]),
  getFindingTaskFacets: vi.fn(async () => ({
    tasks: [
      { id: 't-9', goal: '对 http://target 做 XSS 测试', created_at: '2026-11-14T06:49:00', count: 2 },
      { id: 't-8', goal: '对 http://target 做 XSS 测试', created_at: '2026-11-13T21:05:00', count: 1 },
    ],
    has_more: false,
  })),
}))

vi.mock('@/api/tasks', () => ({
  getTaskDetail: vi.fn(async (id: string) => ({ id, goal: '选中任务的目标', created_at: '2026-11-12T08:00:00' })),
}))

vi.mock('@/api/conversations', () => ({
  getConversation: vi.fn(async (id: string) => ({ id, title: '登录页越权排查', task_id: 't-7' })),
}))

const mockedListFindings = listFindings as unknown as ReturnType<typeof vi.fn>
const mockedUpdateFindingTriage = updateFindingTriage as unknown as ReturnType<typeof vi.fn>
const mockedGetFindingStat = getFindingStat as unknown as ReturnType<typeof vi.fn>

// stat fixture：按行聚合 status/severity，保证 chip 与列表口径一致。
function makeStat(findings: FindingRow[]) {
  const by_status: Record<string, number> = {}
  const by_severity: Record<string, number> = {}
  for (const f of findings) {
    by_status[f.status] = (by_status[f.status] ?? 0) + 1
    const sev = f.severity.toLowerCase()
    by_severity[sev] = (by_severity[sev] ?? 0) + 1
  }
  return { total: findings.length, by_status, by_severity }
}

function makeFinding(overrides: Partial<FindingRow> = {}): FindingRow {
  return {
    id: 'f-1',
    seq: 1,
    severity: 'high',
    summary: 'SQL 注入漏洞',
    host: 'a.example.com',
    class: 'sql_injection',
    class_name: 'SQL 注入',
    cwe: 'CWE-89',
    source: 'direct',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeListResponse(findings: FindingRow[], overrides: Partial<FindingListResponse> = {}): FindingListResponse {
  return { findings, total: findings.length, page: 1, size: 50, ...overrides }
}

// React Query 需要 Provider；筛选/分页状态走 URL 需要 Router（对齐 useTrafficFilters 测试模式）。
// 模拟浏览器后退与地址栏（MemoryRouter 的历史栈）
function HistoryProbe() {
  const navigate = useNavigate()
  return (
    <>
      <output data-testid="location">{useLocation().search}</output>
      <button type="button" onClick={() => navigate(-1)}>
        浏览器后退
      </button>
    </>
  )
}

function renderPage(entry = '/findings') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[entry]}>
        <FindingsPage />
        <HistoryProbe />
        <ConfirmHost />
        <Toaster />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('FindingsPage', () => {
  beforeEach(() => {
    mockedListFindings.mockReset()
    mockedUpdateFindingTriage.mockReset()

    // stat 默认空口径（汇总条测试内显式覆盖）
    mockedGetFindingStat.mockResolvedValue({ total: 0, by_status: {}, by_severity: {} })
  })

  it('挂载时加载 findings 列表', async () => {
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage()
    await waitFor(() => expect(mockedListFindings).toHaveBeenCalled())
    expect(await screen.findByText('SQL 注入漏洞')).toBeTruthy()
  })

  it('无数据时显示空态提示', async () => {
    mockedListFindings.mockResolvedValue(makeListResponse([]))
    renderPage()
    expect(await screen.findByText(/暂无发现/)).toBeTruthy()
  })

  it('加载失败时显示错误', async () => {
    mockedListFindings.mockRejectedValue(new Error('服务器错误'))
    renderPage()
    expect(await screen.findByText(/服务器错误/)).toBeTruthy()
  })

  // 图例按钮：含「标签 span + 数字计数 span」的 button，与堆叠条分段按钮（无文本）、
  // 表格行 severity 徽章（非 button）区分。
  function findLegendButton(container: HTMLElement, label: string): HTMLElement {
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) =>
        Array.from(b.querySelectorAll('span')).some((s) => s.textContent === label) &&
        /\d/.test(b.textContent ?? ''),
    )
    if (!btn) throw new Error(`legend button not found: ${label}`)
    return btn
  }

  it('severity 汇总条统计数正确（本页分布）', async () => {
    const findings = [
      makeFinding({ id: 'f1', severity: 'critical' }),
      makeFinding({ id: 'f2', severity: 'critical' }),
      makeFinding({ id: 'f3', severity: 'high', status: 'confirmed' }),
      makeFinding({ id: 'f4', severity: 'low' }),
    ]
    mockedListFindings.mockResolvedValue(makeListResponse(findings, { total: 4 }))
    // chip 读服务端 /findings/stat（与列表同筛选口径、与翻页无关）
    mockedGetFindingStat.mockResolvedValue(makeStat(findings))
    const { container } = renderPage()
    // 4 条同名 finding，等待表格渲染出全部行而非单条匹配。
    await waitFor(() => expect(screen.getAllByText('SQL 注入漏洞').length).toBe(4))

    // 总数 4（服务端 total；同一数字还出现在分页「共 4 条」，断言存在即可）
    expect(screen.getAllByText('4').length).toBeGreaterThanOrEqual(1)
    // 图例：严重 2 / 高危 1 / 低危 1
    expect(within(findLegendButton(container, '严重')).getByText('2')).toBeTruthy()
    expect(within(findLegendButton(container, '高危')).getByText('1')).toBeTruthy()
    expect(within(findLegendButton(container, '低危')).getByText('1')).toBeTruthy()
  })

  it('关键词搜索交给服务端（覆盖全部页），停顿后发起，并回到第一页', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()], { total: 120 }))
    renderPage('/findings?page=3')
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    await user.type(screen.getByRole('textbox', { name: '搜索发现、主机、路径或任务目标' }), 'xss')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ search: 'xss', page: 1 })),
    )
    expect(mockedGetFindingStat).toHaveBeenCalledWith(expect.objectContaining({ search: 'xss' }))
    // 逐字不发请求：只有停顿后的完整关键词
    expect(mockedListFindings).not.toHaveBeenCalledWith(expect.objectContaining({ search: 'x' }))
  })

  it('status 下拉筛选触发重新加载（走服务端）', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage()
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    const statusSelect = screen.getByDisplayValue('全部状态')
    await user.selectOptions(statusSelect, 'confirmed')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ status: 'confirmed' })),
    )
  })

  it('类型筛选：选项来自服务端分面（展示名 + 数量，「其他」也可筛），选中后按类型重新加载，类型列显示展示名', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage()
    await screen.findByText('SQL 注入漏洞')
    expect(screen.getByRole('cell', { name: 'SQL 注入' })).toBeInTheDocument()
    const classSelect = screen.getByRole('combobox', { name: '按类型筛选' })
    await waitFor(() => expect(within(classSelect).getByRole('option', { name: '敏感信息泄露（9）' })).toBeInTheDocument())
    expect(within(classSelect).getByRole('option', { name: '其他（1）' })).toHaveValue('other')
    expect(vi.mocked(getFindingClassFacets).mock.calls[0]?.[0]).not.toHaveProperty('class')
    mockedListFindings.mockClear()

    await user.selectOptions(classSelect, 'info_disclosure')
    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ class: 'info_disclosure' })),
    )
  })

  it('source 下拉筛选按 assignment.source 取值（direct / proxy）触发重新加载', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage()
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    const sourceSelect = screen.getByRole('combobox', { name: '按来源筛选' })
    await user.selectOptions(sourceSelect, 'proxy')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ source: 'proxy' })),
    )
  })

  it('点击 severity 图例切换筛选（再点取消）', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding({ severity: 'critical' })]))
    // 图例可点前提：stat 里该严重度计数 > 0（n=0 的图例禁用——不可筛选到零条）
    mockedGetFindingStat.mockResolvedValue(makeStat([makeFinding({ severity: 'critical' })]))
    const { container } = renderPage()
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    await user.click(findLegendButton(container, '严重'))
    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ severity: 'critical' })),
    )

    mockedListFindings.mockClear()
    // 重新查询按钮：切换筛选后行/图例重渲染，旧引用可能已从 DOM 分离。
    await user.click(findLegendButton(container, '严重'))
    await waitFor(() => expect(mockedListFindings).toHaveBeenCalled())
    // 再次点击应取消 severity 筛选
    const lastCallArgs = mockedListFindings.mock.calls[mockedListFindings.mock.calls.length - 1][0]
    expect(lastCallArgs.severity).toBe('')
  })

  // 打开的发现写在 URL：跳到任务 / 对话再后退、刷新都还原抽屉；不在当前页也能按 ID 打开。
  it('打开写入 ?selected=，后退关闭抽屉', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding({ summary: '点击测试漏洞' })]))
    renderPage()
    await user.click(await screen.findByText('点击测试漏洞'))
    expect(screen.getByTestId('location').textContent).toBe('?selected=f-1')
    expect(await screen.findByText('发现处置')).toBeTruthy()
    // 模态抽屉打开时页面其余部分不可点（pointer-events: none），后退按钮直接派发点击
    fireEvent.click(screen.getByText('浏览器后退'))
    await waitFor(() => expect(screen.queryByText('发现处置')).toBeNull())
  })

  it('带 ?selected= 进页：不在当前页的发现按 ID 取回并打开', async () => {
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage('/findings?selected=f-9')
    expect(await screen.findByText('不在当前页的发现')).toBeTruthy()
  })

  // 搜索框输入不是导航：不新增历史，后退回到搜索之前
  it('搜索不进历史：后退回到搜索之前', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/findings?severity=high', '/findings']} initialIndex={1}>
          <FindingsPage />
          <HistoryProbe />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await user.type(await screen.findByRole('textbox', { name: '搜索发现、主机、路径或任务目标' }), 'xss')
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('?search=xss'))
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('location').textContent).toBe('?severity=high')
  })

  it('点击表格行打开详情抽屉', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding({ summary: '点击测试漏洞' })]))
    renderPage()
    const row = await screen.findByText('点击测试漏洞')

    await user.click(row)

    // 抽屉标题为 sr-only「发现详情」，处置区块「发现处置」应可见
    expect(await screen.findByText('发现处置')).toBeTruthy()
  })

  it('保存 triage：成功后调用 updateFindingTriage 并重拉列表', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ id: 'f-save', status: 'open', triage_note: '' })
    mockedListFindings.mockResolvedValue(makeListResponse([finding]))
    mockedUpdateFindingTriage.mockResolvedValue({ ...finding, status: 'open', triage_note: '已核实' })
    renderPage()

    const row = await screen.findByText('SQL 注入漏洞')
    await user.click(row)

    const noteBox = await screen.findByPlaceholderText(/处置思路/)
    await user.type(noteBox, '已核实')

    const saveBtn = screen.getByRole('button', { name: '保存处置' })
    await user.click(saveBtn)

    await waitFor(() =>
      expect(mockedUpdateFindingTriage).toHaveBeenCalledWith('f-save', 'open', 'high', '已核实'),
    )
  })

  it('保存 triage 失败时弹出失败通知', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ id: 'f-fail', status: 'open', triage_note: '' })
    mockedListFindings.mockResolvedValue(makeListResponse([finding]))
    mockedUpdateFindingTriage.mockRejectedValue(new Error('保存失败'))
    renderPage()

    const row = await screen.findByText('SQL 注入漏洞')
    await user.click(row)

    const noteBox = await screen.findByPlaceholderText(/处置思路/)
    await user.type(noteBox, '触发失败')

    const saveBtn = screen.getByRole('button', { name: '保存处置' })
    await user.click(saveBtn)

    expect(await screen.findByText('处置保存失败，请重试')).toBeTruthy()
    expect(mockedUpdateFindingTriage).toHaveBeenCalled()
  })

  it('分页：多于一页时显示翻页控件与每页条数选择器，点下一页触发带 page 的重新请求', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()], { total: 120, page: 1, size: 50 }))
    renderPage()
    await screen.findByText('SQL 注入漏洞')

    expect(screen.getByLabelText('每页条数')).toBeTruthy()
    const nextBtn = screen.getByLabelText('下一页')
    expect((nextBtn as HTMLButtonElement).disabled).toBe(false)

    mockedListFindings.mockClear()
    await user.click(nextBtn)

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ page: 2, size: 50 })),
    )
  })
})

describe('FindingsPage 任务筛选', () => {
  beforeEach(() => {
    mockedListFindings.mockReset()
    mockedGetFindingStat.mockReset()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    mockedGetFindingStat.mockResolvedValue({ total: 1, by_status: {}, by_severity: {} })
  })

  it('范围是筛选栏里的可搜索选择框：从任务跳来即选中，列表与统计都只看这个任务', async () => {
    renderPage('/findings?task=t-9')
    expect(await screen.findByRole('button', { name: '范围：任务：选中任务的目标' })).toBeTruthy()
    await waitFor(() => expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ task: 't-9' })))
    expect(mockedGetFindingStat).toHaveBeenCalledWith(expect.objectContaining({ task: 't-9' }))
  })

  it('候选由服务端按目标检索，只随结构化筛选变化；同目标的任务靠创建时间区分；可清除', async () => {
    const user = userEvent.setup()
    renderPage('/findings?task=t-9&source=direct&search=xss')
    await user.click(await screen.findByRole('button', { name: /^范围：/ }))
    const options = await screen.findAllByRole('option', { name: /对 http:\/\/target 做 XSS 测试/ })
    expect(options).toHaveLength(2)
    expect(screen.getByText('11-14 06:49 创建 · 2 个发现')).toBeTruthy()
    expect(screen.getByText('11-13 21:05 创建 · 1 个发现')).toBeTruthy()
    // 候选只随结构化筛选：带来源，不带任务本身与台账关键词
    const [filters, q] = (getFindingTaskFacets as unknown as ReturnType<typeof vi.fn>).mock.calls.at(-1)!
    expect(filters).toEqual(expect.objectContaining({ source: 'direct' }))
    expect(filters.task).toBeUndefined()
    expect(filters.search).toBeUndefined()
    expect(q).toBe('')

    await user.click(screen.getByRole('option', { name: '全部任务' }))
    await waitFor(() => expect(mockedListFindings).toHaveBeenLastCalledWith(expect.objectContaining({ task: '' })))
  })

  // 从对话跳来只看「这个对话的发现」：服务端按对话过滤，范围显示对话标题而不是任务目标。
  it('从对话跳来按对话限定，范围显示对话标题；改选任务即换成按任务限定', async () => {
    const user = userEvent.setup()
    renderPage('/findings?conversation=c-7')
    expect(await screen.findByRole('button', { name: '范围：对话：登录页越权排查' })).toBeTruthy()
    await waitFor(() => expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ conversation: 'c-7' })))
    expect(mockedGetFindingStat).toHaveBeenCalledWith(expect.objectContaining({ conversation: 'c-7' }))

    await user.click(screen.getByRole('button', { name: /^范围：/ }))
    await user.click((await screen.findAllByRole('option', { name: /对 http:\/\/target 做 XSS 测试/ }))[0])
    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenLastCalledWith(expect.objectContaining({ task: 't-9', conversation: '' })),
    )
  })
})
