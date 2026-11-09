import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SkillsPage } from './SkillsPage'
import { createSkill, deleteSkill, getSkill, listSkillsPaged, patchSkill, resetSkill } from '@/api/config'
import type { Skill, SkillDetail, SkillListItem } from '@/api/types'

vi.mock('@/api/config', () => ({
  createSkill: vi.fn(),
  patchSkill: vi.fn(),
  resetSkill: vi.fn(),
  deleteSkill: vi.fn(),
  getSkill: vi.fn(),
  assignSkill: vi.fn(),
  listSkillsPaged: vi.fn(),
}))

const mList = listSkillsPaged as unknown as ReturnType<typeof vi.fn>
const mCreate = createSkill as unknown as ReturnType<typeof vi.fn>
const mPatch = patchSkill as unknown as ReturnType<typeof vi.fn>
const mReset = resetSkill as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteSkill as unknown as ReturnType<typeof vi.fn>
const mGet = getSkill as unknown as ReturnType<typeof vi.fn>

function sk(o: Partial<Skill> = {}): Skill {
  return {
    code: 'bac',
    name: 'BAC 越权检测指南',
    description: '访问控制失效检测方法论',
    category: 'vuln',
    body: '# BAC 指南正文',
    is_builtin: true,
    enabled: true,
    ...o,
  }
}

const item = (s: Skill, o: Partial<SkillListItem> = {}): SkillListItem => ({ ...s, customized: [], shadows_builtin: false, ...o })

function detail(s: Skill, o: Partial<SkillDetail> = {}): SkillDetail {
  return {
    skill: s,
    defaults: s.is_builtin ? { name: s.name, description: s.description, body: s.body, enabled: true } : null,
    overrides: {},
    shadows_builtin: false,
    agents: [],
    ...o,
  }
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <SkillsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('SkillsPage', () => {
  beforeEach(() => {
    for (const m of [mList, mCreate, mPatch, mReset, mDelete, mGet]) m.mockReset()
    mList.mockResolvedValue({ skills: [item(sk())], total: 1 })
    mGet.mockResolvedValue(detail(sk()))
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('列表展示来源与自定义标记', async () => {
    mList.mockResolvedValue({ skills: [item(sk(), { customized: ['body'] })], total: 1 })
    renderPage()
    expect(await screen.findByText('BAC 越权检测指南')).toBeTruthy()
    expect(screen.getByText('内置')).toBeTruthy()
    expect(screen.getByText('已自定义')).toBeTruthy()
  })

  it('内置技能：标识与分类只读、不可删除；修改只提交改动字段', async () => {
    const user = userEvent.setup()
    mPatch.mockResolvedValue(detail(sk()))
    renderPage()
    await user.click(await screen.findByRole('button', { name: /BAC 越权检测指南/ }))
    const name = await screen.findByLabelText('名称', { exact: false })
    expect((screen.getByLabelText('标识符', { exact: false }) as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByLabelText('分类', { exact: false }) as HTMLInputElement).disabled).toBe(true)
    expect(screen.queryByText('删除')).toBeNull()
    await user.type(name, ' v2')
    await user.click(screen.getByText('保存'))
    await waitFor(() => expect(mPatch).toHaveBeenCalledWith('bac', { name: 'BAC 越权检测指南 v2' }))
  })

  it('内置技能已自定义的字段可恢复出厂', async () => {
    const user = userEvent.setup()
    const custom = detail(sk({ body: '我改过的正文' }), {
      overrides: { body: { kind: 'replace', default: '# BAC 指南正文', baseline: null, default_changed: false, matches_default: false } },
    })
    mGet.mockResolvedValue(custom)
    mReset.mockResolvedValue(detail(sk()))
    renderPage()
    await user.click(await screen.findByRole('button', { name: /BAC 越权检测指南/ }))
    await user.click(await screen.findByRole('tab', { name: '知识内容' }))
    expect(screen.getByText('升级前的修改')).toBeTruthy()
    await user.click(screen.getByText('恢复默认'))
    await waitFor(() => expect(mReset).toHaveBeenCalledWith('bac', 'body'))
    await waitFor(() => expect((screen.getByLabelText(/技能文档/) as HTMLTextAreaElement).value).toBe('# BAC 指南正文'))
  })

  it('自建技能可删除', async () => {
    const user = userEvent.setup()
    const mine = sk({ code: 'mine', name: '我的技能', is_builtin: false })
    mList.mockResolvedValue({ skills: [item(mine)], total: 1 })
    mGet.mockResolvedValue(detail(mine))
    mDelete.mockResolvedValue(undefined)
    renderPage()
    await user.click(await screen.findByRole('button', { name: /我的技能/ }))
    await user.click(await screen.findByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('mine'))
  })

  it('新建技能：必填校验 + createSkill 载荷', async () => {
    const user = userEvent.setup()
    mCreate.mockResolvedValue(sk({ code: 'xss', is_builtin: false }))
    renderPage()
    await screen.findByText('BAC 越权检测指南')
    await user.click(screen.getByText('新建'))
    expect((screen.getByText('保存').closest('button') as HTMLButtonElement).disabled).toBe(true)
    await user.type(screen.getByLabelText('标识符', { exact: false }), 'xss')
    await user.type(screen.getByLabelText('名称', { exact: false }), 'XSS 指南')
    await user.type(screen.getByLabelText('分类', { exact: false }), 'vuln')
    await user.click(screen.getByRole('tab', { name: '知识内容' }))
    await user.type(screen.getByLabelText(/技能文档/), '# XSS 方法论')
    await user.click(screen.getByText('保存'))
    await waitFor(() => expect(mCreate).toHaveBeenCalled())
    expect(mCreate.mock.calls[0][0]).toEqual({
      code: 'xss',
      name: 'XSS 指南',
      description: '',
      category: 'vuln',
      body: '# XSS 方法论',
    })
  })

  it('搜索走服务端 q 参数（防抖后带 query 重取）', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('BAC 越权检测指南')
    mList.mockClear()
    await user.type(screen.getByPlaceholderText('搜索名称 / 标识 / 描述'), 'bac')
    await waitFor(() => expect(mList).toHaveBeenCalledWith(1, expect.any(Number), 'bac'))
  })
})

describe('SkillsPage 抽屉竞态', () => {
  beforeEach(() => {
    mList.mockReset()
    mGet.mockReset()
    mList.mockResolvedValue({
      skills: [item(sk({ code: 'aaa', name: '技能甲' })), item(sk({ code: 'bbb', name: '技能乙' }))],
      total: 2,
    })
  })

  it('先开 A 再开 B：A 的迟到详情不得覆盖 B', async () => {
    const user = userEvent.setup()
    let resolveA!: (v: unknown) => void
    mGet.mockImplementation((code: string) =>
      code === 'aaa'
        ? new Promise((r) => (resolveA = r))
        : Promise.resolve(detail(sk({ code: 'bbb', name: '技能乙' }), { agents: [{ code: 'planner', name: 'B-规划', involved: true }] })),
    )
    renderPage()
    await user.click(await screen.findByRole('button', { name: /技能甲/ }))
    await user.click(await screen.findByRole('button', { name: /技能乙/ }))
    expect(await screen.findByText('B-规划')).toBeTruthy()

    resolveA(detail(sk({ code: 'aaa' }), { agents: [{ code: 'executor', name: 'A-执行', involved: false }] }))
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByText('A-执行')).toBeNull()
    expect(screen.getByText('B-规划')).toBeTruthy()
  })
})
