import { afterEach, describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TasksPage } from './TasksPage'
import { getTaskDetail, listTasks } from '@/api/tasks'
import { listSchedules } from '@/api/schedules'
import type { TaskSummary } from '@/api/types'

vi.mock('@/api/tasks', () => ({
  listTasks: vi.fn(),
  getTaskDetail: vi.fn(),
  createTasks: vi.fn(),
}))

vi.mock('@/api/conversations', () => ({
  getConversation: vi.fn(async (id: string) => ({ id, title: '登录页越权排查', task_id: 't-1' })),
}))

vi.mock('@/api/schedules', () => ({
  listSchedules: vi.fn(),
  setScheduleEnabled: vi.fn(),
  deleteSchedule: vi.fn(),
}))

const mList = listTasks as unknown as ReturnType<typeof vi.fn>
const mSchedules = listSchedules as unknown as ReturnType<typeof vi.fn>
const mDetail = getTaskDetail as unknown as ReturnType<typeof vi.fn>

function task(o: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't-1',
    goal: 'DVWA 渗透测试',
    target_host: 'dvwa.local',
    status: 'active',
    paused: false,
    assignment_id: 'a-1',
    assignment_title: '批次 A',
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
    created_at: '2026-06-10T00:00:00Z',
    ended_at: null,
    ...o,
  }
}

// 模拟浏览器后退与地址栏（MemoryRouter 的历史栈）
function HistoryProbe() {
  const navigate = useNavigate()
  const location = useLocation()
  return (
    <>
      <button type="button" onClick={() => navigate(-1)}>
        浏览器后退
      </button>
      <output data-testid="location">{location.search}</output>
    </>
  )
}

function renderPage(initialEntries: string[] = ['/tasks']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={initialEntries}>
        <TasksPage />
        <HistoryProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('TasksPage（任务模块）', () => {
  afterEach(() => vi.unstubAllGlobals())
  beforeEach(() => {
    mList.mockReset()
    mDetail.mockReset()
  })

  it('挂载拉取任务列表并渲染行（目标可见）', async () => {
    mList.mockResolvedValue({
      items: [task(), task({ id: 't-2', goal: 'BAC 越权检测', status: 'completed' })],
      total: 2,
      page: 1,
      size: 20,
    })
    renderPage()

    expect(await screen.findByText('DVWA 渗透测试')).toBeTruthy()
    expect(screen.getByText('BAC 越权检测')).toBeTruthy()
    expect(mList).toHaveBeenCalled()
  })

  // 从其它页面跳来只看这一个任务：列表按范围筛选，范围显示在筛选栏里，进页即打开详情；
  // 清除范围回到全部任务。
  it('从对话跳来：列表只剩这个对话的任务并打开详情，筛选栏显示对话标题，可清除', async () => {
    mList.mockImplementation(async (_p: number, _s: number, f?: { conversation?: string }) =>
      f?.conversation
        ? { items: [task()], total: 1, page: 1, size: 20 }
        : { items: [task(), task({ id: 't-2', goal: 'BAC 越权检测' })], total: 2, page: 1, size: 20 },
    )
    mDetail.mockResolvedValue(task())
    const user = userEvent.setup()
    renderPage(['/tasks?conversation=c-7&selected=t-1'])

    await waitFor(() => expect(mList).toHaveBeenCalledWith(1, expect.any(Number), expect.objectContaining({ conversation: 'c-7' })))
    expect(await screen.findByLabelText('关闭详情')).toBeTruthy()
    expect(screen.queryByText('BAC 越权检测')).toBeNull()

    await user.click(await screen.findByRole('button', { name: /^清除范围（对话：登录页越权排查）/ }))
    expect(await screen.findByText('BAC 越权检测')).toBeTruthy()
  })

  it('从漏洞等页面跳来：按任务限定，筛选栏显示任务目标', async () => {
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    mDetail.mockResolvedValue(task())
    renderPage(['/tasks?task=t-1'])
    await waitFor(() => expect(mList).toHaveBeenCalledWith(1, expect.any(Number), expect.objectContaining({ task: 't-1' })))
    expect(await screen.findByRole('button', { name: /^清除范围（任务：DVWA 渗透测试）/ })).toBeTruthy()
  })

  // 打开的任务写在 URL：刷新、跳走再后退都还原；打开是一步操作，后退即关闭。
  it('点行打开详情写入 ?selected=，后退关闭详情；带 ?selected= 进页直接打开', async () => {
    mList.mockResolvedValue({ items: [task(), task({ id: 't-2', goal: 'BAC 越权检测' })], total: 2, page: 1, size: 20 })
    mDetail.mockImplementation(async (id: string) => task({ id }))
    stubViewport(1440)
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText('BAC 越权检测'))
    expect(screen.getByTestId('location').textContent).toBe('?selected=t-2')
    expect(await screen.findByLabelText('关闭详情')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: '浏览器后退' }))
    await waitFor(() => expect(screen.queryByLabelText('关闭详情')).toBeNull())
    expect(screen.getByTestId('location').textContent).toBe('')
  })

  it('窄屏：详情由 ?selected= 决定，后退回到列表', async () => {
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    mDetail.mockResolvedValue(task())
    stubViewport(390)
    const user = userEvent.setup()
    renderPage(['/tasks', '/tasks?selected=t-1'])
    expect(await screen.findByLabelText('关闭详情')).toBeTruthy()
    expect(screen.queryByRole('search', { name: '任务筛选' })).toBeNull()
    await user.click(screen.getByRole('button', { name: '浏览器后退' }))
    expect(await screen.findByRole('search', { name: '任务筛选' })).toBeTruthy()
  })

  // 搜索匹配看得见的字段：目标与批次名（批次 ID 界面上不显示）。
  it('搜索框按目标或批次名检索', async () => {
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    renderPage()
    expect(await screen.findByRole('textbox', { name: '搜索目标或批次名' })).toBeTruthy()
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

  it('页头「定时模板」打开模板管理抽屉', async () => {
    const user = userEvent.setup()
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    mSchedules.mockResolvedValue([])
    renderPage()

    await user.click(await screen.findByRole('button', { name: '定时模板' }))
    expect(await screen.findByRole('dialog', { name: '定时模板' })).toBeTruthy()
    expect(await screen.findByText(/还没有定时模板/)).toBeTruthy()
  })

  it('定时任务详情的「定时」标记打开抽屉并定位到来源模板', async () => {
    const user = userEvent.setup()
    Element.prototype.scrollIntoView = vi.fn()
    const scheduled = task({ scheduled: true, schedule_id: 's-9', cron: '0 4 * * *' })
    mList.mockResolvedValue({ items: [scheduled], total: 1, page: 1, size: 20 })
    mDetail.mockResolvedValue(scheduled)
    mSchedules.mockResolvedValue([
      { id: 's-9', title: '夜间复扫', description: '', cron: '0 4 * * *', enabled: true, briefs: ['b'],
        next_run_at: null, last_run_at: null, created_at: '2026-11-11T00:00:00Z' },
    ])
    renderPage()

    await user.click(await screen.findByText('DVWA 渗透测试'))
    await user.click(await screen.findByRole('button', { name: /定时 · 查看模板/ }))
    const card = (await screen.findByText('夜间复扫')).closest('article')!
    expect(card).toHaveAttribute('aria-current', 'true')
  })

  it('列表请求失败时展示错误信息', async () => {
    mList.mockRejectedValue(new Error('GET 500: 数据库不可用'))
    renderPage()

    expect(await screen.findByText(/GET 500: 数据库不可用/)).toBeTruthy()
  })
})

// 按视口宽度模拟 matchMedia（只解析 min-width 断点）。
function stubViewport(width: number) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => ({
      matches: Number(/min-width:\s*(\d+)px/.exec(q)?.[1] ?? 0) <= width,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  )
}

describe('TasksPage 窄屏（< 768）详情独占', () => {
  beforeEach(() => {
    mList.mockReset()
    mDetail.mockReset()
    mList.mockResolvedValue({ items: [task()], total: 1, page: 1, size: 20 })
    mDetail.mockResolvedValue(task())
  })
  afterEach(() => vi.unstubAllGlobals())

  it('打开详情时列表让位（面板 440px 盖不住窄屏），关闭后列表回来', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText('DVWA 渗透测试'))
    await screen.findByLabelText('关闭详情')
    expect(screen.queryByRole('search', { name: '任务筛选' })).toBeNull()
    await user.click(screen.getByLabelText('关闭详情'))
    expect(await screen.findByRole('search', { name: '任务筛选' })).toBeInTheDocument()
  })

  it('宽屏列表与详情并排', async () => {
    stubViewport(1440)
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText('DVWA 渗透测试'))
    await screen.findByLabelText('关闭详情')
    expect(screen.getByRole('search', { name: '任务筛选' })).toBeInTheDocument()
  })
})
