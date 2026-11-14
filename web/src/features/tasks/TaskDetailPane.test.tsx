import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TaskDetailPane } from './TaskDetailPane'
import type { TaskSummary } from '@/api/types'

function makeTask(overrides: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't-1',
    brief: '测试本地靶机',
    target_host: 'target.local',
    status: 'active',
    paused: false,
    assignment_id: 'a-1',
    assignment_title: '批次',
    assignment_index: 1,
    assignment_total: 1,
    source: 'direct',
    scheduled: false,
    schedule_id: '',
    cron: '',
    conversation_id: 'c-1',
    finding_count: 2,
    step_count: 5,
    tokens_in: 0,
    tokens_out: 0,
    created_at: '2026-01-01T00:00:00Z',
    ended_at: null,
    ...overrides,
  }
}

describe('TaskDetailPane', () => {
  it('跳转区排在任务信息之前：从任务去对话、探索图与漏洞是最先用到的出口', () => {
    render(
      <MemoryRouter>
        <TaskDetailPane task={makeTask()} loading={false} error="" onClose={vi.fn()} />
      </MemoryRouter>,
    )
    const jump = screen.getByRole('heading', { name: '跳转' })
    const info = screen.getByRole('heading', { name: '任务信息' })
    expect(jump.compareDocumentPosition(info) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    for (const name of ['对话', '探索图', '漏洞']) {
      expect(screen.getByRole('link', { name: new RegExp(name) })).toBeTruthy()
    }
  })

  it('没有会话也没有漏洞时不渲染跳转区', () => {
    render(
      <MemoryRouter>
        <TaskDetailPane task={makeTask({ conversation_id: '', finding_count: 0 })} loading={false} error="" onClose={vi.fn()} />
      </MemoryRouter>,
    )
    expect(screen.queryByRole('heading', { name: '跳转' })).toBeNull()
  })
})
