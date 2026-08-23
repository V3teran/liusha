import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AgentAdmin } from './AgentAdmin'
import { listAgentsPaged } from '@/api/client'
import { saveAgent, deleteAgent, listToolCandidates } from '@/api/config'
import type { AgentConfig, Tool, ToolKind } from '@/api/types'

vi.mock('@/api/client', () => ({
  listAgentsPaged: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  saveAgent: vi.fn(),
  deleteAgent: vi.fn(),
  listToolCandidates: vi.fn(),
}))

const mList = listAgentsPaged as unknown as ReturnType<typeof vi.fn>
const mSave = saveAgent as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteAgent as unknown as ReturnType<typeof vi.fn>
const mListCandidates = listToolCandidates as unknown as ReturnType<typeof vi.fn>

// listAgentsPaged 返回 {agents,total} 信封。
const paged = (rows: AgentConfig[]) => ({ agents: rows, total: rows.length })

function tool(name: string, kind: ToolKind, category: string): Tool {
  return { name, kind, category, description: `${name} 描述`, sort_order: 0 }
}

// 两套工具候选：function（内部函数）/ cli（外部 CLI）。按 kind 分派返回。
const FUNCTION_TOOLS = [tool('read_findings', 'function', 'findings'), tool('write_finding', 'function', 'findings')]
const CLI_TOOLS = [tool('nmap', 'cli', 'recon'), tool('nuclei', 'cli', 'vulnscan')]

function h(o: Partial<AgentConfig> = {}): AgentConfig {
  return {
    id: 'h1',
    code: 'recon',
    kind: 'executor',
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

describe('AgentAdmin', () => {
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
    render(<AgentAdmin />)
    expect(await screen.findByText('侦察猎手')).toBeTruthy()
  })

  it('内部工具集从目录读取候选并回填选中态', async () => {
    mList.mockResolvedValue(paged([h()])) // function_tools=['read_findings']
    render(<AgentAdmin />)
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
    render(<AgentAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    await userEvent.click(screen.getByRole('tab', { name: '工具集' }))
    await userEvent.click(await screen.findByText('write_finding'))
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][0].function_tools).toEqual(['read_findings', 'write_finding'])
  })

  it('编排猎手显示编排徽章', async () => {
    mList.mockResolvedValue(paged([h({ kind: 'planner', name: '编排猎手' })]))
    render(<AgentAdmin />)
    expect(await screen.findByText('编排')).toBeTruthy()
  })

  it('删除走确认后调 deleteAgent', async () => {
    mList.mockResolvedValue(paged([h()]))
    mDelete.mockResolvedValue(undefined)
    render(<AgentAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await userEvent.click(await screen.findByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('h1'))
  })

  it('工具候选拉取失败时降级为空、不拖垮智能体列表', async () => {
    mList.mockResolvedValue(paged([h()]))
    mListCandidates.mockRejectedValue(new Error('500'))
    render(<AgentAdmin />)
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
    render(<AgentAdmin />)
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
