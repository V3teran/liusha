import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SchedulesDrawer } from './SchedulesDrawer'
import { deleteSchedule, listSchedules, setScheduleEnabled } from '@/api/schedules'
import { ApiError } from '@/api/client'
import type { ScheduleRow } from '@/api/types'

vi.mock('@/api/schedules', () => ({
  listSchedules: vi.fn(),
  setScheduleEnabled: vi.fn(),
  deleteSchedule: vi.fn(),
}))

const mList = listSchedules as unknown as ReturnType<typeof vi.fn>
const mToggle = setScheduleEnabled as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteSchedule as unknown as ReturnType<typeof vi.fn>

function row(over: Partial<ScheduleRow> = {}): ScheduleRow {
  return {
    id: 's1',
    title: '夜间复扫',
    description: '',
    cron: '0 4 * * *',
    enabled: true,
    briefs: ['复扫 http://a', '复扫 http://b'],
    next_run_at: '2026-11-12T04:00:00Z',
    last_run_at: null,
    created_at: '2026-11-11T00:00:00Z',
    ...over,
  }
}

function renderDrawer(focusId: string | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SchedulesDrawer open onOpenChange={vi.fn()} focusId={focusId} />
    </QueryClientProvider>,
  )
}

describe('SchedulesDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Element.prototype.scrollIntoView = vi.fn()
  })

  it('列出模板：标题、cron 人话、目标清单与启用开关', async () => {
    mList.mockResolvedValue([row(), row({ id: 's2', title: '周报', cron: '0 9 * * 1', enabled: false, next_run_at: null })])
    renderDrawer()
    const card = (await screen.findByText('夜间复扫')).closest('article')!
    expect(within(card).getByText('每天 04:00')).toBeTruthy()
    expect(within(card).getByText('复扫 http://b')).toBeTruthy()
    expect(within(card).getByRole('switch', { name: '启用「夜间复扫」' })).toHaveAttribute('aria-checked', 'true')
    const off = screen.getByText('周报').closest('article')!
    expect(within(off).getByRole('switch')).toHaveAttribute('aria-checked', 'false')
    expect(within(off).getByText('已停用，不会触发')).toBeTruthy()
  })

  it('空态给出创建指引', async () => {
    mList.mockResolvedValue([])
    renderDrawer()
    expect(await screen.findByText(/还没有定时模板/)).toBeTruthy()
  })

  it('切换开关以目标状态调用 API', async () => {
    mList.mockResolvedValue([row()])
    mToggle.mockResolvedValue(row({ enabled: false }))
    const user = userEvent.setup()
    renderDrawer()
    await user.click(await screen.findByRole('switch', { name: '启用「夜间复扫」' }))
    await waitFor(() => expect(mToggle).toHaveBeenCalledWith('s1', false))
  })

  it('删除须经确认，确认后调用 API', async () => {
    mList.mockResolvedValue([row()])
    mDelete.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderDrawer()
    await user.click(await screen.findByRole('button', { name: '删除「夜间复扫」' }))
    expect(mDelete).not.toHaveBeenCalled()
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('s1'))
  })

  it('操作失败展示后端原因', async () => {
    mList.mockResolvedValue([row()])
    mToggle.mockRejectedValue(new ApiError('PATCH', 404, '资源不存在'))
    const user = userEvent.setup()
    renderDrawer()
    await user.click(await screen.findByRole('switch', { name: '启用「夜间复扫」' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('资源不存在')
  })

  it('focusId 指向的模板高亮并滚入视野；已删除的模板给出提示', async () => {
    mList.mockResolvedValue([row()])
    const { unmount } = renderDrawer('s1')
    const card = (await screen.findByText('夜间复扫')).closest('article')!
    expect(card).toHaveAttribute('aria-current', 'true')
    await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalled())
    unmount()

    renderDrawer('gone')
    expect(await screen.findByText(/来源模板已删除/)).toBeTruthy()
  })
})
