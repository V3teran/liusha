import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ConvMenu } from './ConvMenu'
import type { Conversation } from '@/api/types'

const conv = (over: Partial<Conversation>): Conversation =>
  ({ id: 'c-1', title: 't', task_id: 't-1', source: 'direct', ...over }) as Conversation

function renderMenu(c: Conversation) {
  return render(
    <MemoryRouter>
      <ConvMenu conv={c} style={{ top: "0px", left: "0px" }} deleting={null} onClose={vi.fn()} onRename={vi.fn()} onDelete={vi.fn()} />
    </MemoryRouter>,
  )
}

describe('ConvMenu', () => {
  it('漏洞跳到这个会话所属任务的漏洞', () => {
    renderMenu(conv({}))
    expect(screen.getByRole('link', { name: /漏洞/ }).getAttribute('href')).toBe('/findings?task=t-1')
  })

  it('没有关联任务的会话没有漏洞入口', () => {
    renderMenu(conv({ task_id: '' }))
    expect(screen.queryByRole('link', { name: /漏洞/ })).toBeNull()
  })
})
