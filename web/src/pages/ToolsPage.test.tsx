import { afterEach, describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { ToolsPage } from './ToolsPage'
import { listTools, getTool, assignTool } from '@/api/config'
import type { Tool, ToolAgent, ToolDetail, ToolKind } from '@/api/types'

vi.mock('@/api/config', () => ({
  listTools: vi.fn(),
  getTool: vi.fn(),
  assignTool: vi.fn(),
}))

const mList = listTools as unknown as ReturnType<typeof vi.fn>
const mGet = getTool as unknown as ReturnType<typeof vi.fn>
const mAssign = assignTool as unknown as ReturnType<typeof vi.fn>

function agent(code: string, name: string, involved: boolean): ToolAgent {
  return { code, name, involved }
}

function tool(name: string, kind: ToolKind, category: string, categoryName = category): Tool {
  return { name, kind, category, category_name: categoryName, description: `${name} 的描述`, sort_order: 0 }
}

const FN = tool('read_credentials', 'function', 'credentials')
const CLI = tool('nmap', 'cli', 'recon', '信息收集')

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ToolsPage />
    </QueryClientProvider>,
  )
}

function detailOf(t: Tool, agents: ToolAgent[] = []): ToolDetail {
  return { tool: t, agents }
}

describe('ToolsPage', () => {
  beforeEach(() => {
    mList.mockReset()
    mGet.mockReset()
    mAssign.mockReset()
    mGet.mockImplementation((name: string) =>
      Promise.resolve(detailOf(name === 'nmap' ? CLI : FN)),
    )
  })

  it('挂载加载工具目录并渲染分组列表行', async () => {
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    renderPage()
    expect(await screen.findByText('read_credentials')).toBeTruthy()
    expect(screen.getByText('nmap')).toBeTruthy()
    // 分组头用后端下发的分类显示名，不显示分类码。
    expect(screen.getAllByText('信息收集').length).toBeGreaterThan(0)
    expect(screen.queryByText('recon')).toBeNull()
  })

  it('自动选中首项并在右栏常驻面板展示全量智能体（已装配者高亮）', async () => {
    mList.mockResolvedValue({ tools: [FN], total: 1 })
    mGet.mockResolvedValue(
      detailOf(FN, [agent('recon', '侦察智能体', true), agent('web', 'Web 智能体', false)]),
    )
    renderPage()
    // 无需点击：主从视图恒有选中，首项自动加载。
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('read_credentials'))
    expect(screen.getByText(/装配到智能体/)).toBeTruthy()
    // 全量智能体都在（含未装配者）；involved 由后端下发驱动高亮（aria-pressed）。
    const recon = await screen.findByRole('button', { name: '侦察智能体' })
    const web = screen.getByRole('button', { name: 'Web 智能体' })
    expect(recon.getAttribute('aria-pressed')).toBe('true')
    expect(web.getAttribute('aria-pressed')).toBe('false')
  })

  it('点击未装配智能体，回存追加该工具后高亮', async () => {
    mList.mockResolvedValue({ tools: [FN], total: 1 })
    // 假后端：装配写入后，再取详情即反映新的装配态（成功后会重拉权威值）。
    let involved = false
    mGet.mockImplementation(() => Promise.resolve(detailOf(FN, [agent('web', 'Web 智能体', involved)])))
    mAssign.mockImplementation(() => {
      involved = true
      return Promise.resolve(undefined)
    })
    renderPage()
    const web = await screen.findByRole('button', { name: 'Web 智能体' })
    expect(web.getAttribute('aria-pressed')).toBe('false')
    await userEvent.click(web)
    // 调后端装配（工具名、code、involved=true）。
    await waitFor(() => expect(mAssign).toHaveBeenCalledWith('read_credentials', 'web', true))
    // 乐观翻转即时高亮。
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Web 智能体' }).getAttribute('aria-pressed'),
      ).toBe('true'),
    )
  })

  it('点选另一行切换详情', async () => {
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    renderPage()
    await screen.findByText('read_credentials')
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('read_credentials'))
    await userEvent.click(screen.getByText('nmap'))
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('nmap'))
  })

  it('切换种类段触发按 kind 过滤取数', async () => {
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    renderPage()
    await screen.findByText('read_credentials')
    mList.mockResolvedValue({ tools: [CLI], total: 1 })
    // 「外部工具」（KIND_LABEL.cli）既是种类段按钮也是分组头/徽章文案，取文本恰为该值的段按钮。
    const cliSegment = screen
      .getAllByText('外部工具')
      .find((el) => el.tagName === 'BUTTON' && el.textContent === '外部工具')
    expect(cliSegment).toBeTruthy()
    await userEvent.click(cliSegment!)
    await waitFor(() => expect(mList.mock.calls.some((c) => c[0]?.kind === 'cli')).toBe(true))
  })

  // 详情取数失败要说出来并可重试，不能伪装成「未选择」。
  it('详情加载失败显示错误与重试', async () => {
    mList.mockResolvedValue({ tools: [FN], total: 1 })
    mGet.mockRejectedValueOnce(new Error('后端不可用'))
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('后端不可用')
    expect(screen.queryByText('从左侧选择一个工具')).toBeNull()
    mGet.mockResolvedValue(detailOf(FN))
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })

  // 在 A 上装配，未落定前切到 B；A 的写入失败只回滚 A，不把 B 显示成已装配。
  it('装配失败只回滚发起写入的工具', async () => {
    vi.stubGlobal('alert', vi.fn())
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    mGet.mockImplementation((name: string) =>
      Promise.resolve(name === 'nmap' ? detailOf(CLI, [agent('web', 'Web 智能体', false)]) : detailOf(FN, [agent('web', 'Web 智能体', false)])),
    )
    let reject!: (e: Error) => void
    mAssign.mockImplementation(() => new Promise((_r, rj) => (reject = rj)))
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: 'Web 智能体' }))
    await userEvent.click(screen.getByText('nmap'))
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('nmap'))
    reject(new Error('保存失败'))
    await waitFor(() => expect(window.alert).toHaveBeenCalled())
    expect(screen.getByRole('button', { name: 'Web 智能体' }).getAttribute('aria-pressed')).toBe('false')
    vi.unstubAllGlobals()
  })

  it('空态提示', async () => {
    mList.mockResolvedValue({ tools: [], total: 0 })
    renderPage()
    expect(await screen.findByText(/工具目录为空/)).toBeTruthy()
  })
})

// 按视口宽度模拟 matchMedia（只解析 min-width 断点）。
function stubViewport(width: number) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => ({
      matches: Number(/min-width:\s*(\d+)px/.exec(q)?.[1] ?? 0) <= width,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  )
}

describe('ToolsPage 窄屏（< 768）主从单栏', () => {
  beforeEach(() => {
    mList.mockReset()
    mGet.mockReset()
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    mGet.mockImplementation((name: string) => Promise.resolve(detailOf(name === 'nmap' ? CLI : FN)))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('先只显示列表；点选工具进入详情，「返回工具列表」回到列表', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText('nmap'))
    expect(screen.queryByRole('region', { name: '工具列表' })).toBeNull()
    await waitFor(() => expect(mGet).toHaveBeenLastCalledWith('nmap'))
    await user.click(screen.getByRole('button', { name: '返回工具列表' }))
    expect(screen.getByRole('region', { name: '工具列表' })).toBeInTheDocument()
  })

  it('宽屏双栏并存，无返回按钮', async () => {
    stubViewport(1440)
    renderPage()
    expect(await screen.findByRole('region', { name: '工具列表' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '返回工具列表' })).toBeNull()
  })
})
