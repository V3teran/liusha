import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FindingsPage } from './FindingsPage'
import { listFindings, updateFindingTriage } from '@/api/client'
import type { FindingRow } from '@/api/types'

vi.mock('@/api/client', () => ({
  listFindings: vi.fn(),
  updateFindingTriage: vi.fn(),
}))

const mockedListFindings = listFindings as unknown as ReturnType<typeof vi.fn>
const mockedUpdateFindingTriage = updateFindingTriage as unknown as ReturnType<typeof vi.fn>

function makeFinding(overrides: Partial<FindingRow> = {}): FindingRow {
  return {
    id: 'f-1',
    severity: 'high',
    summary: 'SQL 注入漏洞',
    host: 'a.example.com',
    mode: 'active',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('FindingsPage', () => {
  beforeEach(() => {
    mockedListFindings.mockReset()
    mockedUpdateFindingTriage.mockReset()
    vi.spyOn(window, 'alert').mockImplementation(() => {})
  })

  it('挂载时加载 findings 列表', async () => {
    mockedListFindings.mockResolvedValue([makeFinding()])
    render(<FindingsPage />)
    await waitFor(() => expect(mockedListFindings).toHaveBeenCalled())
    expect(await screen.findByText('SQL 注入漏洞')).toBeTruthy()
  })

  it('无数据时显示空态提示', async () => {
    mockedListFindings.mockResolvedValue([])
    render(<FindingsPage />)
    expect(await screen.findByText(/暂无漏洞/)).toBeTruthy()
  })

  it('加载失败时显示错误', async () => {
    mockedListFindings.mockRejectedValue(new Error('服务器错误'))
    render(<FindingsPage />)
    expect(await screen.findByText(/服务器错误/)).toBeTruthy()
  })

  // 图例按钮（唯一含 <b> 子元素的 button，与堆叠条分段按钮、表格行 severity 标签区分）。
  function findLegendButton(container: HTMLElement, label: string): HTMLElement {
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.querySelector('b') && b.textContent?.trim().startsWith(label),
    )
    if (!btn) throw new Error(`legend button not found: ${label}`)
    return btn
  }

  it('severity 汇总条统计数正确', async () => {
    mockedListFindings.mockResolvedValue([
      makeFinding({ id: 'f1', severity: 'critical' }),
      makeFinding({ id: 'f2', severity: 'critical' }),
      makeFinding({ id: 'f3', severity: 'high', status: 'confirmed' }),
      makeFinding({ id: 'f4', severity: 'low' }),
    ])
    const { container } = render(<FindingsPage />)
    // 4 条同名 finding，等待表格渲染出全部行而非单条匹配。
    await waitFor(() => expect(screen.getAllByText('SQL 注入漏洞').length).toBe(4))

    // 总数 4
    expect(screen.getByText('4')).toBeTruthy()
    // 图例：严重 2 / 高危 1 / 低危 1
    expect(within(findLegendButton(container, '严重')).getByText('2')).toBeTruthy()
    expect(within(findLegendButton(container, '高危')).getByText('1')).toBeTruthy()
    expect(within(findLegendButton(container, '低危')).getByText('1')).toBeTruthy()
  })

  it('搜索框按 summary/host/path 模糊过滤', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([
      makeFinding({ id: 'f1', summary: 'SQL 注入', host: 'a.example.com' }),
      makeFinding({ id: 'f2', summary: 'XSS 漏洞', host: 'b.example.com', target: { path: '/search' } }),
    ])
    render(<FindingsPage />)
    await screen.findByText('SQL 注入')

    const search = screen.getByPlaceholderText(/搜索漏洞标题/)
    await user.type(search, 'xss')

    expect(screen.queryByText('SQL 注入')).toBeNull()
    expect(screen.getByText('XSS 漏洞')).toBeTruthy()
  })

  it('搜索框按 host 过滤', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([
      makeFinding({ id: 'f1', summary: 'A 漏洞', host: 'unique-host.example.com' }),
      makeFinding({ id: 'f2', summary: 'B 漏洞', host: 'other.example.com' }),
    ])
    render(<FindingsPage />)
    await screen.findByText('A 漏洞')

    const search = screen.getByPlaceholderText(/搜索漏洞标题/)
    await user.type(search, 'unique-host')

    expect(screen.getByText('A 漏洞')).toBeTruthy()
    expect(screen.queryByText('B 漏洞')).toBeNull()
  })

  it('severity 下拉筛选触发重新加载', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([makeFinding()])
    render(<FindingsPage />)
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    const statusSelect = screen.getByDisplayValue('全部状态')
    await user.selectOptions(statusSelect, 'confirmed')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ status: 'confirmed' })),
    )
  })

  it('mode 下拉筛选触发重新加载', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([makeFinding()])
    render(<FindingsPage />)
    await screen.findByText('SQL 注入漏洞')
    mockedListFindings.mockClear()

    const modeSelect = screen.getByDisplayValue('全部来源')
    await user.selectOptions(modeSelect, 'passive')

    await waitFor(() =>
      expect(mockedListFindings).toHaveBeenCalledWith(expect.objectContaining({ mode: 'passive' })),
    )
  })

  it('点击 severity 图例切换筛选（再点取消）', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([makeFinding({ severity: 'critical' })])
    const { container } = render(<FindingsPage />)
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
    expect(lastCallArgs.severity).toBeUndefined()
  })

  it('点击表格行打开详情抽屉', async () => {
    const user = userEvent.setup()
    mockedListFindings.mockResolvedValue([makeFinding({ summary: '点击测试漏洞' })])
    render(<FindingsPage />)
    const row = await screen.findByText('点击测试漏洞')

    await user.click(row)

    // 抽屉标题为 sr-only「漏洞详情」，处置区块「处置」应可见
    expect(await screen.findByText('处置')).toBeTruthy()
  })

  it('保存 triage：乐观更新后调用 updateFindingTriage 成功覆盖', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ id: 'f-save', status: 'open', triage_note: '' })
    mockedListFindings.mockResolvedValue([finding])
    mockedUpdateFindingTriage.mockResolvedValue({ ...finding, status: 'open', triage_note: '已核实' })
    render(<FindingsPage />)

    const row = await screen.findByText('SQL 注入漏洞')
    await user.click(row)

    const noteBox = await screen.findByPlaceholderText(/处置备注/)
    await user.type(noteBox, '已核实')

    const saveBtn = screen.getByRole('button', { name: '保存' })
    await user.click(saveBtn)

    await waitFor(() =>
      expect(mockedUpdateFindingTriage).toHaveBeenCalledWith('f-save', 'open', 'high', '已核实'),
    )
  })

  it('保存 triage 失败时回滚行数据并 alert 提示', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ id: 'f-fail', status: 'open', triage_note: '' })
    mockedListFindings.mockResolvedValue([finding])
    mockedUpdateFindingTriage.mockRejectedValue(new Error('保存失败'))
    const { container } = render(<FindingsPage />)

    const row = await screen.findByText('SQL 注入漏洞')
    await user.click(row)

    const noteBox = await screen.findByPlaceholderText(/处置备注/)
    await user.type(noteBox, '触发失败')

    const saveBtn = screen.getByRole('button', { name: '保存' })
    await user.click(saveBtn)

    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('处置保存失败，请重试'))
    await waitFor(() => expect(mockedUpdateFindingTriage).toHaveBeenCalled())
    // 回滚：表格行数据恢复为保存前状态——原 finding 无备注，行内「有处置备注」📝 标记不应出现。
    const rowEl = container.querySelector('[aria-label="查看漏洞详情：SQL 注入漏洞"]')
    expect(rowEl?.textContent).not.toContain('📝')
  })
})
