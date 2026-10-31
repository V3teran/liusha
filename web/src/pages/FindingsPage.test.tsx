import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { FindingsPage } from './FindingsPage'
import { getFindingStat, listFindings, updateFindingTriage } from '@/api/findings'
import type { FindingRow, FindingListResponse } from '@/api/types'

vi.mock('@/api/findings', () => ({
  listFindings: vi.fn(),
  updateFindingTriage: vi.fn(),
  getFindingStat: vi.fn(),
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
    source: 'manual',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeListResponse(findings: FindingRow[], overrides: Partial<FindingListResponse> = {}): FindingListResponse {
  return { findings, total: findings.length, page: 1, size: 50, ...overrides }
}

// React Query 需要 Provider；筛选/分页状态走 URL 需要 Router（对齐 useTrafficFilters 测试模式）。
function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/findings']}>
        <FindingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('FindingsPage', () => {
  beforeEach(() => {
    mockedListFindings.mockReset()
    mockedUpdateFindingTriage.mockReset()
    
    vi.spyOn(window, 'alert').mockImplementation(() => {})
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
    expect(await screen.findByText(/暂无漏洞/)).toBeTruthy()
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

  it('搜索框按 summary/host/path 模糊过滤（前端叠加，仅作用于当前页）', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(
      makeListResponse([
        makeFinding({ id: 'f1', summary: 'SQL 注入', host: 'a.example.com' }),
        makeFinding({ id: 'f2', summary: 'XSS 漏洞', host: 'b.example.com', target: { path: '/search' } }),
      ]),
    )
    renderPage()
    await screen.findByText('SQL 注入')

    const search = screen.getByPlaceholderText(/搜索漏洞标题/)
    await user.type(search, 'xss')

    expect(screen.queryByText('SQL 注入')).toBeNull()
    expect(screen.getByText('XSS 漏洞')).toBeTruthy()
  })

  it('搜索框按 host 过滤', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(
      makeListResponse([
        makeFinding({ id: 'f1', summary: 'A 漏洞', host: 'unique-host.example.com' }),
        makeFinding({ id: 'f2', summary: 'B 漏洞', host: 'other.example.com' }),
      ]),
    )
    renderPage()
    await screen.findByText('A 漏洞')

    const search = screen.getByPlaceholderText(/搜索漏洞标题/)
    await user.type(search, 'unique-host')

    expect(screen.getByText('A 漏洞')).toBeTruthy()
    expect(screen.queryByText('B 漏洞')).toBeNull()
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

  it('source 下拉筛选触发重新加载', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding()]))
    renderPage()
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    const sourceSelect = screen.getByDisplayValue('全部来源')
    await user.selectOptions(sourceSelect, 'auto')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ source: 'auto' })),
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

  it('点击表格行打开详情抽屉', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue(makeListResponse([makeFinding({ summary: '点击测试漏洞' })]))
    renderPage()
    const row = await screen.findByText('点击测试漏洞')

    await user.click(row)

    // 抽屉标题为 sr-only「漏洞详情」，处置区块「漏洞处置」应可见
    expect(await screen.findByText('漏洞处置')).toBeTruthy()
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

  it('保存 triage 失败时 alert 提示', async () => {
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

    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('处置保存失败，请重试'))
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
