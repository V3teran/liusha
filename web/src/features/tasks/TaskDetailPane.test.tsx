import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPane } from './TaskDetailPane'
import type { TaskSummary, TaskUsage } from '@/api/types'
import { adjustTaskGoal, assignTaskAction, getTaskUsage } from '@/api/tasks'

vi.mock('@/api/tasks', () => ({
  controlTask: vi.fn(),
  adjustTaskGoal: vi.fn(async () => ({})),
  assignTaskAction: vi.fn(async () => ({ id: 'a-new' })),
  getTaskUsage: vi.fn(),
}))

const usageOf = (over: Partial<TaskUsage> = {}): TaskUsage => ({
  tokens: { in: 0, out: 0, cached: 0 },
  llm_calls: 0,
  llm_latency_ms: 0,
  tool_calls: 0,
  tool_duration_ms: 0,
  elapsed_ms: 0,
  by_role: [],
  ...over,
})

function renderPane(task: TaskSummary) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TaskDetailPane task={task} loading={false} error="" onRetry={vi.fn()} onClose={vi.fn()} />
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
    findings_by_severity: { high: 2 },
    step_count: 5,
    tokens_in: 0,
    tokens_out: 0,
    created_at: '2026-01-01T00:00:00Z',
    ended_at: null,
    abort_reason: '',
    ...overrides,
  }
}

describe('TaskDetailPane', () => {
  beforeEach(() => {
    vi.mocked(getTaskUsage).mockResolvedValue(usageOf())
  })

  it('跳转区排在任务信息之前：从任务去对话、探索图与发现是最先用到的出口', () => {
    renderPane(makeTask())
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

  // 详情一次只看一个任务：状态、来源是纯文字（带颜色点），不套徽章；发现数用气泡（五个严重度的颜色本身就是信息）。
  it('状态与来源是纯文字，不套徽章', () => {
    renderPane(makeTask({ status: 'aborted' }))
    expect(screen.getByText('已中止').tagName).toBe('SPAN')
    expect(screen.getByText('已中止').closest('span')?.className).not.toMatch(/rounded-full/)
    expect(screen.getByText('人工下发').className).toBe('')
  })

  it('发现数用气泡按严重度列出，不再是一个总数', async () => {
    renderPane(makeTask({ finding_count: 3, findings_by_severity: { critical: 1, low: 2 } }))
    expect(await screen.findByText('严重 1')).toBeTruthy()
    expect(screen.getByText('低危 2')).toBeTruthy()
    expect(screen.queryByText('3 个')).toBeNull()
  })

  // 智能体调用情况排在进度上面：一行一个智能体，登记过的身份在前，其余（历史压缩）在后。
  it('智能体调用情况在进度上面：一行一个智能体，写明调用了多少次模型、多少次工具', async () => {
    vi.mocked(getTaskUsage).mockResolvedValue(
      usageOf({
        by_role: [
          { role: 'executor', tokens: { in: 800, out: 200, cached: 0 }, llm_calls: 40, llm_latency_ms: 5000, tool_calls: 89, tool_duration_ms: 9000 },
          { role: 'compaction', tokens: { in: 10, out: 1, cached: 0 }, llm_calls: 1, llm_latency_ms: 100, tool_calls: 0, tool_duration_ms: 0 },
          { role: 'planner', tokens: { in: 400, out: 100, cached: 0 }, llm_calls: 14, llm_latency_ms: 2000, tool_calls: 11, tool_duration_ms: 3000 },
        ],
      }),
    )
    renderPane(makeTask({ step_count: 25 }))
    await screen.findByText('规划')
    const agents = screen.getByText('智能体调用')
    const rows = within(agents.parentElement!).getAllByRole('listitem').map((li) => li.textContent)
    expect(rows).toEqual(['规划调用 14 次 · 工具 11 次↑400 ↓100', '执行调用 40 次 · 工具 89 次↑800 ↓200', '压缩调用 1 次 · 工具 0 次↑10 ↓1'])
    const progress = screen.getByText(/^25 步/)
    expect(agents.compareDocumentPosition(progress) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('耗时与对话页顶栏同一个口径、同一种写法：取用量接口的墙钟', async () => {
    vi.mocked(getTaskUsage).mockResolvedValue(usageOf({ elapsed_ms: 65000 }))
    renderPane(makeTask())
    expect(await screen.findByText('1分05秒')).toBeTruthy()
  })

  it('对话已删除的任务仍可看探索图，只是没有对话入口', () => {
    renderPane(makeTask({ conversation_id: '', finding_count: 0 }))
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

  it('已终止的任务显示终止原因，其它状态不显示', () => {
    const { unmount } = renderPane(
      makeTask({ status: 'aborted', ended_at: '2026-01-01T01:00:00Z', abort_reason: '任务运行超时（timeout）' }),
    )
    expect(screen.getByText('终止原因')).toBeTruthy()
    expect(screen.getByText('任务运行超时（timeout）')).toBeTruthy()
    unmount()
    renderPane(makeTask({ status: 'completed', ended_at: '2026-01-01T01:00:00Z' }))
    expect(screen.queryByText('终止原因')).toBeNull()
  })
})
