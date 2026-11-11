import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentsPage } from './AgentsPage'
import {
  deletePromptArchive,
  getAgent,
  listAgentsPaged,
  listSkillCandidates,
  listToolCandidates,
  patchAgent,
  resetAgent,
} from '@/api/config'
import type { Agent, AgentDetail, AgentListItem, Tool, ToolKind } from '@/api/types'

vi.mock('@/api/config', () => ({
  listAgentsPaged: vi.fn(),
  listToolCandidates: vi.fn(),
  listSkillCandidates: vi.fn(),
  getAgent: vi.fn(),
  patchAgent: vi.fn(),
  resetAgent: vi.fn(),
  deletePromptArchive: vi.fn(),
}))

const mList = listAgentsPaged as unknown as ReturnType<typeof vi.fn>
const mGet = getAgent as unknown as ReturnType<typeof vi.fn>
const mPatch = patchAgent as unknown as ReturnType<typeof vi.fn>
const mReset = resetAgent as unknown as ReturnType<typeof vi.fn>
const mDeleteArchive = deletePromptArchive as unknown as ReturnType<typeof vi.fn>
const mListCandidates = listToolCandidates as unknown as ReturnType<typeof vi.fn>
const mListSkills = listSkillCandidates as unknown as ReturnType<typeof vi.fn>

function tool(name: string, kind: ToolKind, category: string): Tool {
  return { name, kind, category, description: `${name} 描述`, sort_order: 0 }
}

const FUNCTION_TOOLS = [tool('read_credentials', 'function', 'credentials'), tool('write_credential', 'function', 'credentials')]
const CLI_TOOLS = [tool('nmap', 'cli', 'recon'), tool('nuclei', 'cli', 'vulnscan')]

function agent(o: Partial<Agent> = {}): Agent {
  return {
    code: 'recon',
    name: '侦察智能体',
    charter: '出厂章程',
    description: '侦察排头兵',
    function_tools: ['read_credentials'],
    cli_tools: [],
    skills: [],
    max_iterations: 20,
    complexity: 'medium',
    ...o,
  }
}

// 详情：未给 defaults 时出厂值即生效值（无自定义）。
function detail(o: Partial<Agent> = {}, extra: Partial<AgentDetail> = {}): AgentDetail {
  const a = agent(o)
  const { code: _c, name: _n, ...editable } = a
  return { agent: a, defaults: editable, overrides: {}, prompt_archive: null, contract: '输出契约原文', ...extra }
}

const listItem = (a: Agent, o: Partial<AgentListItem> = {}): AgentListItem => ({
  ...a,
  customized: [],
  has_prompt_archive: false,
  ...o,
})

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <AgentsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function setup(d: AgentDetail, item: Partial<AgentListItem> = {}) {
  mList.mockResolvedValue({ agents: [listItem(d.agent, item)], total: 1 })
  mGet.mockResolvedValue(d)
  renderPage()
}

async function openEditor() {
  await userEvent.click(await screen.findByRole('button', { name: /侦察智能体/ }))
  await screen.findByRole('dialog')
  await screen.findByLabelText('调度摘要', { exact: false })
}

const saveBtn = () => screen.getByText('保存').closest('button') as HTMLButtonElement

describe('AgentsPage', () => {
  beforeEach(() => {
    for (const m of [mList, mGet, mPatch, mReset, mDeleteArchive, mListCandidates, mListSkills]) m.mockReset()
    mListCandidates.mockImplementation((kind: ToolKind) => Promise.resolve(kind === 'function' ? FUNCTION_TOOLS : CLI_TOOLS))
    mListSkills.mockResolvedValue([])
    mPatch.mockResolvedValue(detail())
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('列表卡片展示中文名、英文标识与自定义标记', async () => {
    setup(detail(), { customized: ['charter', 'skills'], has_prompt_archive: true })
    const card = await screen.findByRole('button', { name: /侦察智能体/ })
    expect(card.textContent).toContain('recon')
    expect(card.textContent).toContain('已自定义 2 项')
    expect(card.textContent).toContain('待处理的旧提示词')
  })

  it('编辑调度摘要后保存：只提交改动字段，按 code 寻址', async () => {
    setup(detail())
    await openEditor()
    const summary = screen.getByLabelText('调度摘要', { exact: false }) as HTMLInputElement
    expect(saveBtn().disabled).toBe(true)
    await userEvent.clear(summary)
    await userEvent.type(summary, '新分工摘要')
    await userEvent.click(saveBtn())
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    expect(mPatch.mock.calls[0]).toEqual(['recon', { description: '新分工摘要' }])
  })

  it('挑选模式勾选工具：提交 {on, off} 操作', async () => {
    setup(detail())
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '内部工具' }))
    const selected = (await screen.findByText('read_credentials')).closest('button') as HTMLButtonElement
    expect(selected.getAttribute('aria-pressed')).toBe('true')
    await userEvent.click(screen.getByText('write_credential'))
    await userEvent.click(saveBtn())
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    expect(mPatch.mock.calls[0][1]).toEqual({ function_tools: { on: ['write_credential'], off: [] } })
  })

  it('出厂为「全部」：全部勾选且开关打开、无改动', async () => {
    setup(detail({ cli_tools: ['*'] }))
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '外部工具' }))
    for (const name of ['nmap', 'nuclei']) {
      expect(((await screen.findByText(name)).closest('button') as HTMLButtonElement).getAttribute('aria-pressed')).toBe('true')
    }
    expect((screen.getByLabelText('全部（含以后新增）') as HTMLInputElement).checked).toBe(true)
    expect(saveBtn().disabled).toBe(true)
  })

  it('「全部」中取消一项：只提交 off 该项（以后新增的工具仍自动装配）', async () => {
    setup(detail({ cli_tools: ['*'] }))
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '外部工具' }))
    await userEvent.click(await screen.findByText('nuclei'))
    await userEvent.click(saveBtn())
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    expect(mPatch.mock.calls[0][1]).toEqual({ cli_tools: { on: [], off: ['nuclei'] } })
  })

  it('关闭「全部」：取消 "*" 并显式勾选界面上仍勾着的项', async () => {
    setup(detail({ cli_tools: ['*'] }))
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '外部工具' }))
    await userEvent.click(await screen.findByLabelText('全部（含以后新增）'))
    await userEvent.click(screen.getByText('nuclei'))
    await userEvent.click(saveBtn())
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    expect(mPatch.mock.calls[0][1]).toEqual({ cli_tools: { on: ['nmap'], off: ['*'] } })
  })

  it('打开「全部」：勾选 "*" 与全部项', async () => {
    setup(detail())
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '外部工具' }))
    await userEvent.click(await screen.findByLabelText('全部（含以后新增）'))
    await userEvent.click(saveBtn())
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    expect(mPatch.mock.calls[0][1]).toEqual({ cli_tools: { on: ['*', 'nmap', 'nuclei'], off: [] } })
  })

  it('已自定义字段显示状态条；恢复默认后保留其它字段未保存的编辑', async () => {
    const customized = detail(
      { complexity: 'simple' },
      {
        defaults: { ...detail().defaults, complexity: 'medium' },
        overrides: {
          complexity: { kind: 'replace', default: 'medium', baseline: 'medium', default_changed: false, matches_default: false },
        },
      },
    )
    setup(customized)
    mReset.mockResolvedValue(detail({ complexity: 'medium' }))
    await openEditor()
    expect(screen.getByText('已自定义')).toBeTruthy()

    const summary = screen.getByLabelText('调度摘要', { exact: false }) as HTMLInputElement
    await userEvent.clear(summary)
    await userEvent.type(summary, '未保存的摘要')
    await userEvent.click(screen.getByText('恢复默认'))
    await waitFor(() => expect(mReset).toHaveBeenCalledWith('recon', 'complexity'))
    await waitFor(() => expect(screen.queryByText('已自定义')).toBeNull())
    expect((screen.getByLabelText('调度摘要', { exact: false }) as HTMLInputElement).value).toBe('未保存的摘要')
  })

  it('出厂已更新：提示并可对比出厂', async () => {
    setup(
      detail(
        { charter: '我的章程' },
        {
          overrides: {
            charter: { kind: 'replace', default: '新出厂章程', baseline: '旧出厂章程', default_changed: true, matches_default: false },
          },
        },
      ),
    )
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '章程' }))
    expect(screen.getByText('出厂已更新')).toBeTruthy()
    await userEvent.click(screen.getByText('对比出厂'))
    expect(screen.getByText('+ 新出厂章程')).toBeTruthy()
    expect(screen.getAllByText('− 旧出厂章程').length).toBeGreaterThan(0)
  })

  it('章程页：只读展示输出契约；提示词归档可查看并删除', async () => {
    setup(detail({}, { prompt_archive: { prompt: '升级前我写的提示词', archived_at: '2026-11-09T00:00:00Z' } }))
    mDeleteArchive.mockResolvedValue(undefined)
    await openEditor()
    await userEvent.click(screen.getByRole('tab', { name: '章程' }))
    expect(screen.getByText('输出契约原文')).toBeTruthy()
    const archive = screen.getByRole('region', { name: '升级前的自定义提示词' })
    expect(within(archive).getByText('升级前我写的提示词')).toBeTruthy()
    await userEvent.click(within(archive).getByText('我已处理，删除归档'))
    await waitFor(() => expect(mDeleteArchive).toHaveBeenCalledWith('recon'))
    await waitFor(() => expect(screen.queryByRole('region', { name: '升级前的自定义提示词' })).toBeNull())
  })

  it('有未保存更改时关闭需确认：拒绝则留、确认则弃', async () => {
    setup(detail())
    await openEditor()
    await userEvent.type(screen.getByLabelText('调度摘要', { exact: false }), 'x')
    vi.mocked(window.confirm).mockReturnValueOnce(false)
    await userEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.getByRole('dialog')).toBeTruthy()
    vi.mocked(window.confirm).mockReturnValueOnce(true)
    await userEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('最大迭代轮数逐键钳到 ≥1 整数', async () => {
    setup(detail())
    await openEditor()
    const input = screen.getByLabelText('最大迭代轮数', { exact: false }) as HTMLInputElement
    fireEvent.change(input, { target: { value: '-5' } })
    expect(input.value).toBe('1')
    fireEvent.change(input, { target: { value: '3.7' } })
    expect(input.value).toBe('3')
  })

  it('工具候选拉取失败时降级为空、不拖垮页面', async () => {
    mListCandidates.mockRejectedValue(new Error('500'))
    setup(detail())
    await openEditor()
    expect(screen.queryByText('nmap')).toBeNull()
  })
})
