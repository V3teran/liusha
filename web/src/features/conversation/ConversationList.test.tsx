import { createRef } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ConversationList, type ConversationListHandle } from './ConversationList'
import { deleteConversation, listConversations, renameConversation } from '@/api/conversations'
import type { Conversation } from '@/api/types'
import { ConfirmHost } from '@/components/ui/confirm'
import { Toaster } from '@/components/ui/toast'
import { DEFAULT_PAGE_SIZE } from '@/lib/pageSize'

vi.mock('@/api/conversations', () => ({
  listConversations: vi.fn(),
  deleteConversation: vi.fn(),
  renameConversation: vi.fn(),
}))

function mkConv(over: Partial<Conversation>): Conversation {
  return {
    id: 'conv-id-00000001',
    title: '我要扫描 example.com',
    task_id: '',
    
    source: 'direct',
    run_status: 'completed',
    finding_count: 0,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  }
}

// mockPage：listConversations 现在返回 {conversations, hasMore}（分页形态），不再是裸数组。
function mockPage(conversations: Conversation[], hasMore = false) {
  vi.mocked(listConversations).mockResolvedValue({ conversations, hasMore, total: conversations.length })
}

const EMPTY = { noun: '对话', hint: '点击「新对话」发起。' }

function renderList(props: Partial<React.ComponentProps<typeof ConversationList>> = {}) {
  const onSelect = vi.fn()
  const onNew = vi.fn()
  const onDeleted = vi.fn()
  const utils = render(
    <MemoryRouter>
      <ConversationList emptyState={EMPTY} onSelect={onSelect} onNew={onNew} onDeleted={onDeleted} {...props} />
      <ConfirmHost />
      <Toaster />
    </MemoryRouter>,
  )
  return { ...utils, onSelect, onNew, onDeleted }
}

describe('ConversationList', () => {
  beforeEach(() => {
    vi.mocked(listConversations).mockReset()
    vi.mocked(deleteConversation).mockReset()
    vi.mocked(renameConversation).mockReset()
  })

  it('挂载时拉取并渲染会话列表', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 a.com' }), mkConv({ id: 'c2', title: '我要扫描 b.com' })])
    renderList()
    await waitFor(() => {
      expect(screen.getByText(/a\.com/)).toBeTruthy()
      expect(screen.getByText(/b\.com/)).toBeTruthy()
    })
  })

  it('source 过滤下沉到服务端：按 source 调用 listConversations', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 direct-x.com', source: 'direct' })])
    renderList({ source: 'direct' })
    await waitFor(() => {
      expect(listConversations).toHaveBeenCalledWith({ limit: DEFAULT_PAGE_SIZE, offset: 0, source: 'direct', search: '' })
      expect(screen.getByText(/direct-x\.com/)).toBeTruthy()
    })
  })

  // 关键词交给服务端检索（覆盖全部会话，分页与总数同一口径）：在已分页的单页上过滤时，
  // 其它页的会话搜不到，「共 N 条」也与看到的对不上。
  it('搜索交给服务端：停顿后带关键词从第一页重新请求，计数是匹配总数', async () => {
    vi.mocked(listConversations).mockImplementation(async ({ search } = {}) =>
      search
        ? { conversations: [mkConv({ id: 'c-9', title: '我要扫描 shop.local 越权' })], hasMore: false, total: 1 }
        : { conversations: [mkConv({ id: 'c-1', title: '我要扫描 alpha.com' })], hasMore: true, total: 120 },
    )
    renderList({ source: 'direct' })
    await screen.findByText('第 1–1 条 · 共 120 条')

    await userEvent.type(screen.getByLabelText('搜索对话'), 'shop')

    await waitFor(() =>
      expect(listConversations).toHaveBeenLastCalledWith({ limit: DEFAULT_PAGE_SIZE, offset: 0, source: 'direct', search: 'shop' }),
    )
    expect(await screen.findByText('第 1–1 条 · 匹配 1 条')).toBeTruthy()
    expect(screen.getByText(/shop\.local/)).toBeTruthy()
    // 逐字不发请求：只有停顿后的完整关键词
    expect(listConversations).not.toHaveBeenCalledWith(expect.objectContaining({ search: 's' }))
  })

  // 计数文案跟随「产出当前数据的那次请求」：关键词已生效、新结果还没回来时，屏上仍是旧数据，
  // 不能把旧的总数标成「匹配 N 条」。
  it('新结果到达前，计数仍按当前展示的数据标注', async () => {
    let release: () => void = () => {}
    vi.mocked(listConversations).mockImplementation(({ search } = {}) =>
      search
        ? new Promise((resolve) => {
            release = () => resolve({ conversations: [mkConv({ id: 'c-9' })], hasMore: false, total: 1 })
          })
        : Promise.resolve({ conversations: [mkConv({ id: 'c-1' })], hasMore: true, total: 120 }),
    )
    renderList()
    await screen.findByText('第 1–1 条 · 共 120 条')
    await userEvent.type(screen.getByLabelText('搜索对话'), 'shop')
    await waitFor(() => expect(listConversations).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'shop' })))
    expect(screen.queryByText(/匹配 120 条/)).toBeNull()
    expect(screen.getByText('第 1–1 条 · 共 120 条')).toBeTruthy()
    release()
    expect(await screen.findByText('第 1–1 条 · 匹配 1 条')).toBeTruthy()
  })

  it('没有匹配时提示「没有匹配的对话」', async () => {
    vi.mocked(listConversations).mockImplementation(async ({ search } = {}) =>
      search ? { conversations: [], hasMore: false, total: 0 } : { conversations: [mkConv({ id: 'c-1' })], hasMore: false, total: 1 },
    )
    renderList()
    await screen.findByText(/example\.com/)
    await userEvent.type(screen.getByLabelText('搜索对话'), 'zzz')
    expect(await screen.findByText('没有匹配的对话')).toBeTruthy()
  })

  it('点击会话行调用 onSelect', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 click.com' })])
    const { onSelect } = renderList()
    await waitFor(() => expect(screen.getByText(/click\.com/)).toBeTruthy())
    await userEvent.click(screen.getByText(/click\.com/))
    expect(onSelect).toHaveBeenCalledWith('c1')
  })

  it('allowNew=true 时显示新会话按钮，点击调用 onNew', async () => {
    mockPage([])
    const { onNew } = renderList({ allowNew: true })
    const btn = await screen.findByRole('button', { name: '新对话' })
    await userEvent.click(btn)
    expect(onNew).toHaveBeenCalled()
  })

  it('allowNew=false 时只有刷新、没有新建入口', async () => {
    mockPage([])
    renderList({ allowNew: false })
    expect(await screen.findByRole('button', { name: '刷新列表' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '新对话' })).toBeFalsy()
  })

  it('空列表按名词渲染「暂无X」空态与说明', async () => {
    mockPage([])
    renderList({ emptyState: { noun: '代理批次', hint: '把浏览器代理指向 liusha' } })
    await waitFor(() => {
      expect(screen.getByText('暂无代理批次')).toBeTruthy()
      expect(screen.getByText('把浏览器代理指向 liusha')).toBeTruthy()
    })
  })

  it('删除流程：确认后调用 deleteConversation 并触发 onDeleted', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 del.com', run_status: 'completed' })])
    vi.mocked(deleteConversation).mockResolvedValue(undefined)
    const { onDeleted } = renderList()
    await waitFor(() => expect(screen.getByText(/del\.com/)).toBeTruthy())

    // 打开 ⋯ 菜单
    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)
    expect(deleteConversation).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: '确认删除' }))

    await waitFor(() => {
      expect(deleteConversation).toHaveBeenCalledWith('c1')
      expect(onDeleted).toHaveBeenCalledWith('c1')
    })
  })

  it('删除时取消确认则不调用 deleteConversation', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 keep.com', run_status: 'completed' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/keep\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)
    await userEvent.click(await screen.findByRole('button', { name: '取消' }))

    expect(deleteConversation).not.toHaveBeenCalled()
  })

  it('任务进行中的会话：菜单显示禁用态提示，不显示删除按钮', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 active.com', run_status: 'active' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/active\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    expect(await screen.findByText('任务进行中 · 先停止再删')).toBeTruthy()
    expect(screen.queryByText('删除对话')).toBeFalsy()
  })

  it('删除接口返回 TASK_ACTIVE 错误时弹提示', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 race.com', run_status: 'completed' })])
    vi.mocked(deleteConversation).mockRejectedValue(new Error('TASK_ACTIVE'))
    renderList()
    await waitFor(() => expect(screen.getByText(/race\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)
    await userEvent.click(await screen.findByRole('button', { name: '确认删除' }))

    expect(await screen.findByText(/任务进行中，无法删除/)).toBeTruthy()
  })

  it('重命名流程：双击菜单里的重命名，输入新标题回车提交', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 rename-me.com', run_status: 'completed' })])
    vi.mocked(renameConversation).mockResolvedValue(undefined)
    renderList()
    await waitFor(() => expect(screen.getByText(/rename-me\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const renameBtn = await screen.findByText('重命名')
    await userEvent.click(renameBtn)

    const input = screen.getByDisplayValue('rename-me.com')
    await userEvent.clear(input)
    await userEvent.type(input, '新标题{Enter}')

    await waitFor(() => {
      expect(renameConversation).toHaveBeenCalledWith('c1', '新标题')
    })
  })

  it('重命名失败时回滚标题并弹提示', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 rollback.com', run_status: 'completed' })])
    vi.mocked(renameConversation).mockRejectedValue(new Error('fail'))
    renderList()
    await waitFor(() => expect(screen.getByText(/rollback\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const renameBtn = await screen.findByText('重命名')
    await userEvent.click(renameBtn)

    const input = screen.getByDisplayValue('rollback.com')
    await userEvent.clear(input)
    await userEvent.type(input, '临时标题{Enter}')

    expect(await screen.findByText('重命名失败，已恢复原标题')).toBeTruthy()
    await waitFor(() => {
      expect(screen.getByText(/rollback\.com/)).toBeTruthy()
    })
  })

  it('Escape 取消重命名不提交', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 esc.com', run_status: 'completed' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/esc\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const renameBtn = await screen.findByText('重命名')
    await userEvent.click(renameBtn)

    const input = screen.getByDisplayValue('esc.com')
    await userEvent.type(input, '{Escape}')

    expect(renameConversation).not.toHaveBeenCalled()
    expect(screen.getByText(/esc\.com/)).toBeTruthy()
  })

  it('forwardRef 暴露 refresh()，调用后回第 1 页重新拉取', async () => {
    vi.mocked(listConversations)
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c1', title: '我要扫描 first.com' })], hasMore: false, total: 1 })
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c2', title: '我要扫描 second.com' })], hasMore: false, total: 1 })

    const ref = createRef<ConversationListHandle>()
    render(
      <MemoryRouter>
        <ConversationList ref={ref} emptyState={EMPTY} onSelect={vi.fn()} onNew={vi.fn()} onDeleted={vi.fn()} />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByText(/first\.com/)).toBeTruthy())

    await ref.current!.refresh()

    await waitFor(() => expect(screen.getByText(/second\.com/)).toBeTruthy())
    expect(screen.queryByText(/first\.com/)).toBeFalsy()
    expect(listConversations).toHaveBeenCalledTimes(2)
  })

  it('⋯ 菜单 portal 渲染到 body，仍可被 RTL 查询到', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 portal.com', run_status: 'completed' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/portal\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const menuBtn = await screen.findByText('重命名')
    // 菜单节点存在于 document.body 而非渲染容器内。
    expect(document.body.contains(menuBtn)).toBe(true)
  })

  it('发现计数徽章：FindingCount > 0 时显示', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 findings.com', finding_count: 3 })])
    renderList()
    await waitFor(() => {
      expect(screen.getByTitle('3 个发现')).toBeTruthy()
    })
  })

  it('每页条数选择框显示的就是实际请求的条数', async () => {
    mockPage([mkConv({ id: 'c1' })])
    renderList()
    await waitFor(() => expect(listConversations).toHaveBeenCalled())
    const limit = vi.mocked(listConversations).mock.calls[0][0]?.limit
    expect((screen.getByRole('combobox', { name: '每页条数' }) as HTMLSelectElement).value).toBe(String(limit))
  })

  it('翻页：hasMore=true 时显示下一页按钮，点击后拉取 offset=PAGE_SIZE', async () => {
    vi.mocked(listConversations)
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c1', title: '我要扫描 page1.com' })], hasMore: true, total: 1 })
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c2', title: '我要扫描 page2.com' })], hasMore: false, total: 1 })

    renderList()
    await waitFor(() => expect(screen.getByText(/page1\.com/)).toBeTruthy())

    const nextBtn = screen.getByRole('button', { name: '下一页' })
    expect(nextBtn).not.toBeDisabled()
    await userEvent.click(nextBtn)

    await waitFor(() => expect(listConversations).toHaveBeenCalledWith({ limit: DEFAULT_PAGE_SIZE, offset: DEFAULT_PAGE_SIZE, source: '', search: '' }))
    await waitFor(() => expect(screen.getByText(/page2\.com/)).toBeTruthy())
    expect(screen.queryByText(/page1\.com/)).toBeFalsy()
  })

  it('翻页：单页（offset=0 且 hasMore=false）时翻页常显但按钮禁用', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 only.com' })], false)
    renderList()
    await waitFor(() => expect(screen.getByText(/only\.com/)).toBeTruthy())
    // 常显总页数（「第 X / Y 页」），单页时前后按钮都在且禁用
    expect(screen.getByText(/第 1\/1 页/)).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
  })

  it('翻页：首页时「上一页」禁用', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 first-disabled.com' })], true)
    renderList()
    await waitFor(() => expect(screen.getByText(/first-disabled\.com/)).toBeTruthy())
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
  })
})

describe('ConversationList 请求竞态', () => {
  it('切换来源后，迟到的旧来源响应不得覆盖新列表', async () => {
    let resolveOld!: (v: { conversations: Conversation[]; hasMore: boolean; total: number }) => void
    vi.mocked(listConversations)
      .mockImplementationOnce(() => new Promise((r) => (resolveOld = r)))
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'p1', title: '代理批次 new.com' })], hasMore: false, total: 1 })
    const { rerender } = renderList({ source: 'direct' })
    rerender(<MemoryRouter><ConversationList source="proxy" emptyState={EMPTY} onSelect={vi.fn()} onNew={vi.fn()} onDeleted={vi.fn()} /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText(/new\.com/)).toBeTruthy())

    resolveOld({ conversations: [mkConv({ id: 'd1', title: '旧来源 stale.com' })], hasMore: false, total: 1 })
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByText(/stale\.com/)).toBeNull()
    expect(screen.getByText(/new\.com/)).toBeTruthy()
  })
})
