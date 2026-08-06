import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HunterAdmin } from './HunterAdmin'
import { listHuntersPaged } from '@/api/client'
import { saveHunter, deleteHunter, listToolCandidates } from '@/api/config'
import type { HunterConfig, Tool, ToolKind } from '@/api/types'

vi.mock('@/api/client', () => ({
  listHuntersPaged: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  saveHunter: vi.fn(),
  deleteHunter: vi.fn(),
  listToolCandidates: vi.fn(),
}))

const mList = listHuntersPaged as unknown as ReturnType<typeof vi.fn>
const mSave = saveHunter as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteHunter as unknown as ReturnType<typeof vi.fn>
const mListCandidates = listToolCandidates as unknown as ReturnType<typeof vi.fn>

// listHuntersPaged 返回 {hunters,total} 信封。
const paged = (rows: HunterConfig[]) => ({ hunters: rows, total: rows.length })

function tool(name: string, kind: ToolKind, category: string): Tool {
  return { name, kind, category, description: `${name} 描述`, sort_order: 0 }
}

// 两套工具候选：function（内部函数）/ cli（外部 CLI）。按 kind 分派返回。
const FUNCTION_TOOLS = [tool('read_findings', 'function', 'findings'), tool('write_finding', 'function', 'findings')]
const CLI_TOOLS = [tool('nmap', 'cli', 'recon'), tool('nuclei', 'cli', 'vulnscan')]

function h(o: Partial<HunterConfig> = {}): HunterConfig {
  return {
    id: 'h1',
    code: 'recon',
    kind: 'domain',
    name: '侦察猎手',
    description: '',
    body: 'charter',
    function_tools: ['read_findings'],
    cli_tools: [],
    max_iterations: 20,
    enabled: true,
    ...o,
  }
}

describe('HunterAdmin', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    mListCandidates.mockReset()
    mListCandidates.mockImplementation((kind: ToolKind) =>
      Promise.resolve(kind === 'function' ? FUNCTION_TOOLS : CLI_TOOLS),
    )
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载猎手列表', async () => {
    mList.mockResolvedValue(paged([h()]))
    render(<HunterAdmin />)
    expect(await screen.findByText('侦察猎手')).toBeTruthy()
  })

  it('内部工具集从目录读取候选并回填选中态', async () => {
    mList.mockResolvedValue(paged([h()])) // function_tools=['read_findings']
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    await userEvent.click(screen.getByRole('tab', { name: '工具集' }))
    // 两个函数工具候选都渲染为可勾选标签；已选项 aria-pressed=true。
    const selected = (await screen.findByText('read_findings')).closest('button') as HTMLButtonElement
    expect(selected.getAttribute('aria-pressed')).toBe('true')
    const unselected = screen.getByText('write_finding').closest('button') as HTMLButtonElement
    expect(unselected.getAttribute('aria-pressed')).toBe('false')
  })

  it('勾选内部函数工具后保存进 function_tools', async () => {
    mList.mockResolvedValue(paged([h()])) // 初值 function_tools=['read_findings']
    mSave.mockResolvedValue(h())
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    await userEvent.click(screen.getByRole('tab', { name: '工具集' }))
    await userEvent.click(await screen.findByText('write_finding'))
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][0].function_tools).toEqual(['read_findings', 'write_finding'])
  })

  it('编排猎手显示编排徽章', async () => {
    mList.mockResolvedValue(paged([h({ kind: 'orchestrator', name: '编排猎手' })]))
    render(<HunterAdmin />)
    expect(await screen.findByText('编排')).toBeTruthy()
  })

  it('删除走确认后调 deleteHunter', async () => {
    mList.mockResolvedValue(paged([h()]))
    mDelete.mockResolvedValue(undefined)
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await userEvent.click(await screen.findByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('h1'))
  })

  it('工具候选拉取失败时降级为空、不拖垮智能体列表', async () => {
    mList.mockResolvedValue(paged([h()]))
    mListCandidates.mockRejectedValue(new Error('500'))
    render(<HunterAdmin />)
    // 主体列表仍渲染，不进整页错误态。
    expect(await screen.findByText('侦察猎手')).toBeTruthy()
    await userEvent.click(screen.getByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    // 候选降级为空：白名单区显示空态占位而非工具按钮。
    expect(screen.queryByText('nmap')).toBeNull()
  })

  it('勾选外部工具白名单后保存进 cli_tools', async () => {
    mList.mockResolvedValue(paged([h()])) // 初值 cli_tools=[]
    mSave.mockResolvedValue(h())
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    await userEvent.click(screen.getByRole('tab', { name: '工具集' }))
    // 候选来自 listToolCandidates('cli')，点 nmap 加入白名单。
    await userEvent.click(await screen.findByText('nmap'))
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][0].cli_tools).toEqual(['nmap'])
  })
})
