import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TasksPage } from './TasksPage'
import { createTasks, getTaskDetail, listTasks } from '@/api/tasks'
import type { TaskSummary } from '@/api/types'

vi.mock('@/api/tasks', () => ({
  listTasks: vi.fn(),
  getTaskDetail: vi.fn(),
  createTasks: vi.fn(),
}))

const mList = listTasks as unknown as ReturnType<typeof vi.fn>
const mDetail = getTaskDetail as unknown as ReturnType<typeof vi.fn>

function task(o: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't-1',
    brief: 'DVWA 渗透测试',
    target_host: 'dvwa.local',
    status: 'active',
    assignment_id: 'a-1',
    assignment_index: 1,
    assignment_total: 1,
    source: 'manual',
    scheduled: false,
    cron: '',
    conversation_id: 'c-1',
    finding_count: 2,
    step_count: 5,
    created_at: '2026-06-10T00:00:00Z',
    ended_at: null,
    ...o,
  }
}

function renderPage(initialEntries: string[] = ['/tasks']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={initialEntries}>
        <TasksPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('TasksPage（任务模块）', () => {
  beforeEach(() => {
    mList.mockReset()
    mDetail.mockReset()
  })

  it('挂载拉取任务列表并渲染行（brief 可见）', async () => {
    mList.mockResolvedValue({
      items: [task(), task({ id: 't-2', brief: 'BAC 越权检测', status: 'completed' })],
      total: 2,
      page: 1,
      size: 20,
    })
    renderPage()

    expect(await screen.findByText('DVWA 渗透测试')).toBeTruthy()
    expect(screen.getByText('BAC 越权检测')).toBeTruthy()
    expect(mList).toHaveBeenCalled()
  })

  it('点击行打开右侧详情板（行数据先撑住，详情请求按 id 发出）', async () => {
    const user = userEvent.setup()
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    mDetail.mockResolvedValue(task())
    renderPage()

    await user.click(await screen.findByText('DVWA 渗透测试'))

    // 详情板出现（关闭按钮唯一锚点），并按 id 拉全量详情
    await screen.findByLabelText('关闭详情')
    await waitFor(() => expect(mDetail).toHaveBeenCalledWith('t-1'))
  })

  it('列表请求失败时展示错误信息', async () => {
    mList.mockRejectedValue(new Error('GET 500: 数据库不可用'))
    renderPage()

    expect(await screen.findByText(/GET 500: 数据库不可用/)).toBeTruthy()
  })
})
