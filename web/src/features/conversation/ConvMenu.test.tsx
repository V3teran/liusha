import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ConvMenu } from './ConvMenu'
import type { Conversation } from '@/api/types'

const conv = (over: Partial<Conversation>): Conversation =>
  ({ id: 'c-1', title: 't', task_id: 't-1', source: 'direct', finding_count: 2, ...over }) as Conversation

function renderMenu(c: Conversation) {
  return render(
    <MemoryRouter>
      <ConvMenu conv={c} style={{ top: "0px", left: "0px" }} deleting={null} onClose={vi.fn()} onRename={vi.fn()} onDelete={vi.fn()} />
    </MemoryRouter>,
  )
}

describe('ConvMenu', () => {
  // 从对话出发只看这个对话的：任务、漏洞、探索图都按对话限定（落点显示对话标题）。
  it('任务、漏洞、探索图都跳到「这个对话的」', () => {
    renderMenu(conv({}))
    expect(screen.getByRole('link', { name: '任务' }).getAttribute('href')).toBe('/tasks?conversation=c-1')
    expect(screen.getByRole('link', { name: '漏洞' }).getAttribute('href')).toBe('/findings?conversation=c-1')
    expect(screen.getByRole('link', { name: '探索图' }).getAttribute('href')).toBe('/exploration-graph?conversation=c-1')
  })

  it('纯聊天会话（没有任务）没有任务与探索图入口', () => {
    renderMenu(conv({ task_id: '' }))
    expect(screen.queryByRole('link', { name: /探索图/ })).toBeNull()
    expect(screen.queryByRole('link', { name: /任务/ })).toBeNull()
  })

  it('没有关联任务或没有漏洞的会话没有漏洞入口（无洞不跳）', () => {
    renderMenu(conv({ task_id: '' }))
    expect(screen.queryByRole('link', { name: /漏洞/ })).toBeNull()
    renderMenu(conv({ finding_count: 0 }))
    expect(screen.queryByRole('link', { name: /漏洞/ })).toBeNull()
  })
})
