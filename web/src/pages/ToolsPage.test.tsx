import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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

function tool(name: string, kind: ToolKind, category: string): Tool {
  return { name, kind, category, description: `${name} 的描述`, sort_order: 0 }
}

const FN = tool('read_findings', 'function', 'findings')
const CLI = tool('nmap', 'cli', 'recon')

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
    render(<ToolsPage />)
    expect(await screen.findByText('read_findings')).toBeTruthy()
    expect(screen.getByText('nmap')).toBeTruthy()
  })

  it('自动选中首项并在右栏常驻面板展示全量智能体（已装配者高亮）', async () => {
    mList.mockResolvedValue({ tools: [FN], total: 1 })
    mGet.mockResolvedValue(
      detailOf(FN, [agent('recon', '侦察智能体', true), agent('web', 'Web 智能体', false)]),
    )
    render(<ToolsPage />)
    // 无需点击：主从视图恒有选中，首项自动加载。
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('read_findings'))
    expect(screen.getByText(/装配到智能体/)).toBeTruthy()
    // 全量智能体都在（含未装配者）；involved 由后端下发驱动高亮（aria-pressed）。
    const recon = await screen.findByRole('button', { name: '侦察智能体' })
    const web = screen.getByRole('button', { name: 'Web 智能体' })
    expect(recon.getAttribute('aria-pressed')).toBe('true')
    expect(web.getAttribute('aria-pressed')).toBe('false')
  })

  it('点击未装配智能体，回存追加该工具后高亮', async () => {
    mList.mockResolvedValue({ tools: [FN], total: 1 })
    mGet.mockResolvedValue(detailOf(FN, [agent('web', 'Web 智能体', false)]))
    mAssign.mockResolvedValue(undefined)
    render(<ToolsPage />)
    const web = await screen.findByRole('button', { name: 'Web 智能体' })
    expect(web.getAttribute('aria-pressed')).toBe('false')
    await userEvent.click(web)
    // 调后端装配（工具名、code、involved=true）。
    await waitFor(() => expect(mAssign).toHaveBeenCalledWith('read_findings', 'web', true))
    // 乐观翻转即时高亮。
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Web 智能体' }).getAttribute('aria-pressed'),
      ).toBe('true'),
    )
  })

  it('点选另一行切换详情', async () => {
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    render(<ToolsPage />)
    await screen.findByText('read_findings')
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('read_findings'))
    await userEvent.click(screen.getByText('nmap'))
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('nmap'))
  })

  it('切换种类段触发按 kind 过滤取数', async () => {
    mList.mockResolvedValue({ tools: [FN, CLI], total: 2 })
    render(<ToolsPage />)
    await screen.findByText('read_findings')
    mList.mockResolvedValue({ tools: [CLI], total: 1 })
    // 「外部 CLI」既是种类段按钮也是分组头/徽章文案，取文本恰为该值的段按钮。
    const cliSegment = screen
      .getAllByText('外部 CLI')
      .find((el) => el.tagName === 'BUTTON' && el.textContent === '外部 CLI')
    expect(cliSegment).toBeTruthy()
    await userEvent.click(cliSegment!)
    await waitFor(() => expect(mList.mock.calls.some((c) => c[0]?.kind === 'cli')).toBe(true))
  })

  it('空态提示', async () => {
    mList.mockResolvedValue({ tools: [], total: 0 })
    render(<ToolsPage />)
    expect(await screen.findByText(/工具目录为空/)).toBeTruthy()
  })
})
