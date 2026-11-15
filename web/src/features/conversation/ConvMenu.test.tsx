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
      <ConvMenu conv={c} style={{ top: "0px", left: "0px" }} deleting={null} onRename={vi.fn()} onDelete={vi.fn()} />
    </MemoryRouter>,
  )
}

describe('ConvMenu', () => {
  // 行菜单只放对话行的操作；跳转是对话自身的出口，在对话顶栏（不重复）。
  it('只有重命名与删除，没有跳转', () => {
    renderMenu(conv({}))
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(screen.getByRole('button', { name: /重命名/ })).toBeTruthy()
    expect(screen.getByRole('button', { name: /删除/ })).toBeTruthy()
  })
})
