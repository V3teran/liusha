import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPane } from './TaskDetailPane'
import type { TaskSummary } from '@/api/types'
import { adjustTaskGoal, assignTaskAction } from '@/api/tasks'

vi.mock('@/api/tasks', () => ({
  controlTask: vi.fn(),
  adjustTaskGoal: vi.fn(async () => ({})),
  assignTaskAction: vi.fn(async () => ({ id: 'a-new' })),
}))

function renderPane(task: TaskSummary) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TaskDetailPane task={task} loading={false} error="" onClose={vi.fn()} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function makeTask(overrides: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't-1',
    goal: '测试本地靶机',
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
    scenario: 'pentest',
    scenario_name: '渗透测试',
    scenario_deleted: false,
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
  it('跳转区排在任务信息之前：从任务去对话、探索图与发现是最先用到的出口', () => {
    render(
      <MemoryRouter>
        <TaskDetailPane task={makeTask()} loading={false} error="" onClose={vi.fn()} />
      </MemoryRouter>,
    )
    const jump = screen.getByRole('heading', { name: '跳转' })
    const info = screen.getByRole('heading', { name: '任务信息' })
    expect(jump.compareDocumentPosition(info) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    for (const name of ['对话', '探索图', '发现']) {
      expect(screen.getByRole('link', { name: new RegExp(name) })).toBeTruthy()
    }
    // 发现、探索图都属于任务：按任务 ID 跳，不经对话
    expect(screen.getByRole('link', { name: /发现/ }).getAttribute('href')).toBe('/findings?task=t-1')
    expect(screen.getByRole('link', { name: /探索图/ }).getAttribute('href')).toBe('/exploration-graph?task=t-1')
  })

  it('对话已删除的任务仍可看探索图，只是没有对话入口', () => {
    render(
      <MemoryRouter>
        <TaskDetailPane task={makeTask({ conversation_id: '', finding_count: 0 })} loading={false} error="" onClose={vi.fn()} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: /探索图/ }).getAttribute('href')).toBe('/exploration-graph?task=t-1')
    expect(screen.queryByRole('link', { name: /对话/ })).toBeNull()
    expect(screen.queryByRole('link', { name: /发现/ })).toBeNull()
  })
})

describe('TaskDetailPane 运行中引导', () => {
  beforeEach(() => vi.clearAllMocks())

  it('调整目标：预填当前目标，提交去首尾空白后的新目标', async () => {
    renderPane(makeTask())
    fireEvent.click(screen.getByRole('button', { name: '调整目标' }))
    const field = screen.getByLabelText('任务目标') as HTMLTextAreaElement
    expect(field.value).toBe('测试本地靶机')
    const save = screen.getByRole('button', { name: '保存目标' }) as HTMLButtonElement
    expect(save.disabled).toBe(true) // 未改动不可提交
    fireEvent.change(field, { target: { value: '  改测注册页  ' } })
    fireEvent.click(save)
    await waitFor(() => expect(adjustTaskGoal).toHaveBeenCalledWith('t-1', '改测注册页'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('指派动作：指令必填，优先级缺省 P1', async () => {
    renderPane(makeTask({ paused: true }))
    fireEvent.click(screen.getByRole('button', { name: '指派动作' }))
    const submit = screen.getByRole('button', { name: '指派' }) as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    fireEvent.change(screen.getByLabelText('指令'), { target: { value: '复测登录接口' } })
    expect((screen.getByLabelText('优先级') as HTMLSelectElement).value).toBe('high')
    fireEvent.click(submit)
    await waitFor(() => expect(assignTaskAction).toHaveBeenCalledWith('t-1', '复测登录接口', 'high'))
  })

  it('已结束的任务不能引导，提示在对话中续接', () => {
    renderPane(makeTask({ status: 'completed', ended_at: '2026-01-01T01:00:00Z' }))
    expect((screen.getByRole('button', { name: '调整目标' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: '指派动作' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('任务已结束，可在对话中续接')).toBeTruthy()
  })
})
