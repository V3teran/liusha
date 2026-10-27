import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import SkillsPage from './SkillsPage'
import { listSkillsPaged } from '@/api/client'
import { createSkill, updateSkill, deleteSkill } from '@/api/config'
import type { Skill } from '@/api/types'

vi.mock('@/api/client', () => ({
  listSkillsPaged: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  createSkill: vi.fn(),
  updateSkill: vi.fn(),
  deleteSkill: vi.fn(),
}))

const mList = listSkillsPaged as unknown as ReturnType<typeof vi.fn>
const mCreate = createSkill as unknown as ReturnType<typeof vi.fn>
const mUpdate = updateSkill as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteSkill as unknown as ReturnType<typeof vi.fn>

function sk(o: Partial<Skill> = {}): Skill {
  return {
    id: 's1',
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

const paged = (skills: Skill[], total = skills.length) => ({ skills, total })

async function tab_debug(screen: typeof import('@testing-library/react').screen) {
  console.log('DBG-TAB', JSON.stringify({
    dialogs: screen.queryAllByRole('dialog').length,
    bodyHead: document.body.textContent.slice(0, 80),
  }));
}

function renderPage() {
  return render(
    <MemoryRouter>
      <SkillsPage />
    </MemoryRouter>,
  )
}

describe('SkillsPage（技能 CRUD）', () => {
  beforeEach(() => {
    mList.mockReset()
    mCreate.mockReset()
    mUpdate.mockReset()
    mDelete.mockReset()
    mList.mockResolvedValue(paged([sk()]))
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载技能列表（分类/内置徽章）', async () => {
    renderPage()
    expect(await screen.findByText('BAC 越权检测指南')).toBeTruthy()
    expect(screen.getByText('内置')).toBeTruthy()
  })

  it('内置技能抽屉：可编辑（仅标识符只读、无删除按钮），改名走 updateSkill', async () => {
    const user = userEvent.setup()
    mUpdate.mockResolvedValue(sk())
    renderPage()
    await user.click(await screen.findByRole('button', { name: /BAC 越权检测指南/ }))
    await screen.findByText('基本信息') // 抽屉打开（默认基本信息 tab）

    // 内置技能：标识符只读、无「删除」按钮（内置拒删），其余字段可编辑
    expect((screen.getByLabelText('标识符', { exact: false }) as HTMLInputElement).disabled).toBe(true)
    expect(screen.queryByText('删除')).toBeNull()
    const name = screen.getByLabelText('名称', { exact: false })
    expect(name).toBeEnabled()
    await user.type(name, ' v2')

    await user.click(screen.getByText('保存'))
    await waitFor(() => expect(mUpdate).toHaveBeenCalledWith('bac', { name: 'BAC 越权检测指南 v2' }))
  })

  it('自建技能抽屉：可编辑 + 删除走 confirm 后 deleteSkill', async () => {
    const user = userEvent.setup()
    mList.mockResolvedValue(paged([sk({ is_builtin: false })]))
    mDelete.mockResolvedValue(undefined)
    renderPage()
    await user.click(await screen.findByRole('button', { name: /BAC 越权检测指南/ }))
    await screen.findByText('基本信息') // 抽屉打开（基本信息 tab 激活）

    // 自建：名称可编辑（hint 同在 label 内，子串匹配）、有删除按钮
    expect((screen.getByLabelText('名称', { exact: false }) as HTMLInputElement).disabled).toBe(false)
    expect(screen.getByText('删除')).toBeTruthy()

    await user.click(screen.getByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('bac'))
  })

  it('新建技能：表单校验 + createSkill 载荷（enabled 默认 true）', async () => {
    const user = userEvent.setup()
    mCreate.mockResolvedValue(sk({ code: 'xss', is_builtin: false }))
    renderPage()
    await screen.findByText('BAC 越权检测指南')
    await user.click(screen.getByText('新建'))

    // 必填未满 → 保存禁用
    expect((screen.getByText('保存').closest('button') as HTMLButtonElement).disabled).toBe(true)
    // 必填未满 → 保存禁用
    await user.click(screen.getByText('保存'))

    // 填全必填（Field 的 label 含 hint 文本 → 子串匹配）
    await user.type(screen.getByLabelText('标识符', { exact: false }), 'xss')
    await user.type(screen.getByLabelText('名称', { exact: false }), 'XSS 指南')
    await user.type(screen.getByLabelText('分类', { exact: false }), 'vuln')
    // body 在「知识内容」tab（Radix inactive 卸载，先切 tab 再填）
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
      enabled: true,
    })
  })

  it('搜索走服务端 q 参数（防抖后带 query 重取）', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('BAC 越权检测指南')
    mList.mockClear()

    await user.type(screen.getByPlaceholderText('搜索名称 / 描述'), 'bac')
    await waitFor(() => expect(mList).toHaveBeenCalledWith(1, expect.any(Number), 'bac'))
  })
})
