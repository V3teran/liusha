import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ConvMenu } from './ConvMenu'
import type { Conversation } from '@/api/types'

const conv = (over: Partial<Conversation>): Conversation =>
  ({ id: 'c-1', title: 't', task_id: 't-1', source: 'direct', finding_count: 2, ...over }) as Conversation

async function openMenu(c: Conversation, handlers: { onRename?: () => void; onDelete?: () => void } = {}) {
  const user = userEvent.setup()
  render(
    <MemoryRouter>
      <ConvMenu conv={c} deleting={null} onRename={handlers.onRename ?? vi.fn()} onDelete={handlers.onDelete ?? vi.fn()} />
    </MemoryRouter>,
  )
  await user.click(screen.getByRole('button', { name: '更多' }))
  // 菜单的可访问名称取自触发按钮（「更多」）
  await screen.findByRole('menu', { name: '更多' })
  return user
}

describe('ConvMenu', () => {
  // 从对话出发只看这个对话的：任务、发现、探索图、模型日志都按对话限定（落点显示对话标题）
  it('任务、发现、探索图、模型日志都跳到「这个对话的」', async () => {
    await openMenu(conv({}))
    expect(screen.getByRole('menuitem', { name: '任务' }).getAttribute('href')).toBe('/tasks?conversation=c-1&selected=t-1')
    expect(screen.getByRole('menuitem', { name: '发现' }).getAttribute('href')).toBe('/findings?conversation=c-1')
    expect(screen.getByRole('menuitem', { name: '探索图' }).getAttribute('href')).toBe('/exploration-graph?conversation=c-1')
    // 模型日志按对话筛：含对话里直接发起的调用（问答、意图分类、标题生成）
    expect(screen.getByRole('menuitem', { name: '模型日志' }).getAttribute('href')).toBe('/llm-audit?conversation=c-1')
  })

  // 纯聊天没有任务：任务相关的去处没有；模型日志照有（问答的调用记在它名下）
  it('纯聊天对话（没有任务）：去处只有模型日志', async () => {
    await openMenu(conv({ task_id: '' }))
    expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['模型日志', '重命名', '删除对话'])
  })

  it('没有发现的对话没有发现入口（无洞不跳）', async () => {
    await openMenu(conv({ finding_count: 0 }))
    expect(screen.queryByRole('menuitem', { name: '发现' })).toBeNull()
  })

  it('重命名、删除交给列表处理', async () => {
    const onRename = vi.fn()
    const user = await openMenu(conv({}), { onRename })
    await user.click(screen.getByRole('menuitem', { name: '重命名' }))
    expect(onRename).toHaveBeenCalledWith(expect.objectContaining({ id: 'c-1' }))
  })

  it('任务进行中不可删除', async () => {
    await openMenu(conv({ run_status: 'active' }))
    expect(screen.getByRole('menuitem', { name: /任务进行中/ }).getAttribute('aria-disabled')).toBe('true')
  })
})
