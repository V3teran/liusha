import { createRef } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ConversationList, type ConversationListHandle } from './ConversationList'
import { deleteConversation, listConversations, renameConversation } from '@/api/conversations'
import type { Conversation } from '@/api/types'

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
    
    Source: 'manual',
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

function renderList(props: Partial<React.ComponentProps<typeof ConversationList>> = {}) {
  const onSelect = vi.fn()
  const onNew = vi.fn()
  const onDeleted = vi.fn()
  const utils = render(
    <MemoryRouter>
      <ConversationList onSelect={onSelect} onNew={onNew} onDeleted={onDeleted} {...props} />
    </MemoryRouter>,
  )
  return { ...utils, onSelect, onNew, onDeleted }
}

describe('ConversationList', () => {
  beforeEach(() => {
    vi.mocked(listConversations).mockReset()
    vi.mocked(deleteConversation).mockReset()
    vi.mocked(renameConversation).mockReset()
    vi.spyOn(window, 'confirm').mockReset()
    vi.spyOn(window, 'alert').mockReset()
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
    mockPage([mkConv({ id: 'c1', title: '我要扫描 manual-x.com', Source: 'manual' })])
    renderList({ source: 'manual' })
    await waitFor(() => {
      expect(listConversations).toHaveBeenCalledWith(30, 0, 'manual')
      expect(screen.getByText(/manual-x\.com/)).toBeTruthy()
    })
  })

  it('搜索框按标题过滤（本页内）', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 alpha.com' }), mkConv({ id: 'c2', title: '我要扫描 beta.com' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/alpha\.com/)).toBeTruthy())

    const search = screen.getByLabelText('搜索对话')
    await userEvent.type(search, 'beta')
    expect(screen.queryByText(/alpha\.com/)).toBeFalsy()
    expect(screen.getByText(/beta\.com/)).toBeTruthy()
  })

  it('搜索框按 id 前缀过滤', async () => {
    mockPage([mkConv({ id: 'abc12345', title: '' }), mkConv({ id: 'zzz99999', title: '' })])
    renderList()
    await waitFor(() => expect(screen.getByText('abc12345')).toBeTruthy())

    const search = screen.getByLabelText('搜索对话')
    await userEvent.type(search, 'abc')
    expect(screen.getByText('abc12345')).toBeTruthy()
    expect(screen.queryByText('zzz99999')).toBeFalsy()
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

  it('allowNew=false 时不显示新建按钮，显示 heading', async () => {
    mockPage([])
    renderList({ allowNew: false, heading: '会话列表' })
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: '新对话' })).toBeFalsy()
    })
    expect(screen.getByText('会话列表')).toBeTruthy()
  })

  it('空列表显示 emptyHint', async () => {
    mockPage([])
    renderList({ emptyHint: '还没有会话哦' })
    await waitFor(() => {
      expect(screen.getByText('还没有会话哦')).toBeTruthy()
    })
  })

  it('删除流程：确认后调用 deleteConversation 并触发 onDeleted', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 del.com', run_status: 'completed' })])
    vi.mocked(deleteConversation).mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { onDeleted } = renderList()
    await waitFor(() => expect(screen.getByText(/del\.com/)).toBeTruthy())

    // 打开 ⋯ 菜单
    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)

    await waitFor(() => {
      expect(deleteConversation).toHaveBeenCalledWith('c1')
      expect(onDeleted).toHaveBeenCalledWith('c1')
    })
    expect(window.confirm).toHaveBeenCalled()
  })

  it('删除时取消确认则不调用 deleteConversation', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 keep.com', run_status: 'completed' })])
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    renderList()
    await waitFor(() => expect(screen.getByText(/keep\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)

    expect(deleteConversation).not.toHaveBeenCalled()
  })

  it('活跃扫描会话：菜单显示禁用态提示，不显示删除按钮', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 active.com', run_status: 'active' })])
    renderList()
    await waitFor(() => expect(screen.getByText(/active\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    expect(await screen.findByText('扫描中 · 先停止再删')).toBeTruthy()
    expect(screen.queryByText('删除对话')).toBeFalsy()
  })

  it('删除接口返回 SCAN_ACTIVE 错误时弹提示', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 race.com', run_status: 'completed' })])
    vi.mocked(deleteConversation).mockRejectedValue(new Error('SCAN_ACTIVE'))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    renderList()
    await waitFor(() => expect(screen.getByText(/race\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const delBtn = await screen.findByText('删除对话')
    await userEvent.click(delBtn)

    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('扫描进行中'))
    })
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
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    renderList()
    await waitFor(() => expect(screen.getByText(/rollback\.com/)).toBeTruthy())

    await userEvent.click(screen.getByTitle('更多'))
    const renameBtn = await screen.findByText('重命名')
    await userEvent.click(renameBtn)

    const input = screen.getByDisplayValue('rollback.com')
    await userEvent.clear(input)
    await userEvent.type(input, '临时标题{Enter}')

    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith('重命名失败，请重试')
    })
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
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c1', title: '我要扫描 first.com' })], hasMore: false })
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c2', title: '我要扫描 second.com' })], hasMore: false })

    const ref = createRef<ConversationListHandle>()
    render(
      <MemoryRouter>
        <ConversationList ref={ref} onSelect={vi.fn()} onNew={vi.fn()} onDeleted={vi.fn()} />
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

  it('漏洞计数徽章：FindingCount > 0 时显示', async () => {
    mockPage([mkConv({ id: 'c1', title: '我要扫描 findings.com', finding_count: 3 })])
    renderList()
    await waitFor(() => {
      expect(screen.getByTitle('3 个漏洞')).toBeTruthy()
    })
  })

  it('翻页：hasMore=true 时显示下一页按钮，点击后拉取 offset=PAGE_SIZE', async () => {
    vi.mocked(listConversations)
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c1', title: '我要扫描 page1.com' })], hasMore: true })
      .mockResolvedValueOnce({ conversations: [mkConv({ id: 'c2', title: '我要扫描 page2.com' })], hasMore: false })

    renderList()
    await waitFor(() => expect(screen.getByText(/page1\.com/)).toBeTruthy())

    const nextBtn = screen.getByRole('button', { name: '下一页' })
    expect(nextBtn).not.toBeDisabled()
    await userEvent.click(nextBtn)

    await waitFor(() => expect(listConversations).toHaveBeenCalledWith(30, 30, ''))
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
