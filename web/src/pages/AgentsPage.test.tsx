import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { AgentsPage } from './AgentsPage'
import { listAgentsPaged } from '@/api/client'
import { saveAgent, listToolCandidates, listSkillCandidates } from '@/api/config'
import type { AgentConfig, Tool, ToolKind } from '@/api/types'

vi.mock('@/api/client', () => ({
  listAgentsPaged: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  saveAgent: vi.fn(),
  listToolCandidates: vi.fn(),
  listSkillCandidates: vi.fn(),
}))

const mList = listAgentsPaged as unknown as ReturnType<typeof vi.fn>
const mSave = saveAgent as unknown as ReturnType<typeof vi.fn>
const mListCandidates = listToolCandidates as unknown as ReturnType<typeof vi.fn>
const mListSkills = listSkillCandidates as unknown as ReturnType<typeof vi.fn>

// listAgentsPaged 返回 {agents,total} 信封。
const paged = (rows: AgentConfig[]) => ({ agents: rows, total: rows.length })

function tool(name: string, kind: ToolKind, category: string): Tool {
  return { name, kind, category, description: `${name} 描述`, sort_order: 0 }
}

// 两套工具候选：function（内部函数）/ cli（外部 CLI）。按 kind 分派返回。
const FUNCTION_TOOLS = [tool('read_findings', 'function', 'findings'), tool('write_finding', 'function', 'findings')]
const CLI_TOOLS = [tool('nmap', 'cli', 'recon'), tool('nuclei', 'cli', 'vulnscan')]

// 复杂度档 hint 内嵌跨页 Link（→ 模型页），组件须在路由上下文中渲染。
const renderPage = () => render(<MemoryRouter><AgentsPage /></MemoryRouter>)

function h(o: Partial<AgentConfig> = {}): AgentConfig {
  return {
    id: 'h1',
    code: 'recon',
    name: '侦察智能体',
    description: '侦察排头兵',
    system_prompt: 'charter',
    function_tools: ['read_findings'],
    cli_tools: [],
    skills: [],
    max_iterations: 20,
    complexity: 'medium',
    enabled: true,
    ...o,
  }
}

// 打开首行的编辑抽屉并停在「基本信息」分页。
async function openEditor() {
  await userEvent.click(await screen.findByText('侦察智能体'))
  await screen.findByText('编辑智能体')
}

describe('AgentsPage', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mListCandidates.mockReset()
    mListCandidates.mockImplementation((kind: ToolKind) =>
      Promise.resolve(kind === 'function' ? FUNCTION_TOOLS : CLI_TOOLS),
    )
    mListSkills.mockReset()
    mListSkills.mockResolvedValue([])
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载智能体列表', async () => {
    mList.mockResolvedValue(paged([h()]))
    renderPage()
    expect(await screen.findByText('侦察智能体')).toBeTruthy()
  })

  it('固定身份字段只读：标识符/名称/调度摘要/启用均 disabled', async () => {
    mList.mockResolvedValue(paged([h()]))
    renderPage()
    await openEditor()
    // name/disabled 由 Field 的 label 包裹结构解析（hint 同在 label 内，用子串匹配）。
    const ro = (label: string) =>
      (screen.getByLabelText(label, { exact: false }) as HTMLInputElement).disabled
    expect(ro('标识符')).toBe(true)
    expect(ro('名称')).toBe(true)
    expect(ro('调度摘要')).toBe(true)
    expect((screen.getByRole('checkbox') as HTMLInputElement).disabled).toBe(true)
  })

  it('内部工具集从目录读取候选并回填选中态', async () => {
    mList.mockResolvedValue(paged([h()])) // function_tools=['read_findings']
    renderPage()
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '内部工具' }))
    // 两个函数工具候选都渲染为可勾选标签；已选项 aria-pressed=true。
    const selected = (await screen.findByText('read_findings')).closest('button') as HTMLButtonElement
    expect(selected.getAttribute('aria-pressed')).toBe('true')
    const unselected = screen.getByText('write_finding').closest('button') as HTMLButtonElement
    expect(unselected.getAttribute('aria-pressed')).toBe('false')
  })

  it('无变更时保存禁用，产生变更后解锁', async () => {
    mList.mockResolvedValue(paged([h()]))
    renderPage()
    await openEditor()
    const saveBtn = () => screen.getByText('保存').closest('button') as HTMLButtonElement
    expect(saveBtn().disabled).toBe(true)
    await userEvent.click(screen.getByRole('tab', { name: '内部工具' }))
    await userEvent.click(await screen.findByText('write_finding'))
    expect(saveBtn().disabled).toBe(false)
  })

  it('勾选内部函数工具后保存：只提交脏字段 function_tools', async () => {
    mList.mockResolvedValue(paged([h()])) // 初值 function_tools=['read_findings']
    mSave.mockResolvedValue(undefined)
    renderPage()
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '内部工具' }))
    await userEvent.click(await screen.findByText('write_finding'))
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][0]).toBe('h1')
    // 只提交脏字段：未动过的 system_prompt/complexity/… 不出现在载荷（后端部分更新语义下不被零值覆盖）。
    expect(Object.keys(mSave.mock.calls[0][1])).toEqual(['function_tools'])
    expect(mSave.mock.calls[0][1].function_tools).toEqual(['read_findings', 'write_finding'])
  })

  it('勾选外部工具白名单后保存进 cli_tools', async () => {
    mList.mockResolvedValue(paged([h()])) // 初值 cli_tools=[]
    mSave.mockResolvedValue(undefined)
    renderPage()
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '外部工具' }))
    // 候选来自 listToolCandidates('cli')，点 nmap 加入白名单。
    await userEvent.click(await screen.findByText('nmap'))
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(Object.keys(mSave.mock.calls[0][1])).toEqual(['cli_tools'])
    expect(mSave.mock.calls[0][1].cli_tools).toEqual(['nmap'])
  })

  it('有未保存更改时关闭需确认：拒绝则留、确认则弃', async () => {
    mList.mockResolvedValue(paged([h()]))
    renderPage()
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '内部工具' }))
    await userEvent.click(await screen.findByText('write_finding'))

    vi.mocked(window.confirm).mockReturnValueOnce(false)
    await userEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.getByText('编辑智能体')).toBeTruthy() // 抽屉仍开

    vi.mocked(window.confirm).mockReturnValueOnce(true)
    await userEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByText('编辑智能体')).toBeNull())
  })

  it('最大迭代轮数逐键钳到 ≥1 整数', async () => {
    mList.mockResolvedValue(paged([h()]))
    renderPage()
    await openEditor()
    const input = screen.getByLabelText('最大迭代轮数', { exact: false }) as HTMLInputElement
    fireEvent.change(input, { target: { value: '-5' } })
    expect(input.value).toBe('1')
    fireEvent.change(input, { target: { value: '3.7' } })
    expect(input.value).toBe('3')
    fireEvent.change(input, { target: { value: '' } })
    expect(input.value).toBe('1')
  })

  it('工具候选拉取失败时降级为空、不拖垮智能体列表', async () => {
    mList.mockResolvedValue(paged([h()]))
    mListCandidates.mockRejectedValue(new Error('500'))
    renderPage()
    // 主体列表仍渲染，不进整页错误态。
    expect(await screen.findByText('侦察智能体')).toBeTruthy()
    await userEvent.click(screen.getByText('侦察智能体'))
    await screen.findByText('编辑智能体')
    // 候选降级为空：白名单区显示空态占位而非工具按钮。
    expect(screen.queryByText('nmap')).toBeNull()
  })
})
