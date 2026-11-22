import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { NewTaskDialog } from './NewTaskDialog'
import { createTasks } from '@/api/tasks'

vi.mock('@/api/tasks', () => ({ createTasks: vi.fn() }))
vi.mock('@/features/scenarios/ScenarioSelect', () => ({ ScenarioSelect: () => null }))

const mCreate = createTasks as unknown as ReturnType<typeof vi.fn>

function renderDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <NewTaskDialog open onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe('NewTaskDialog', () => {
  beforeEach(() => {
    mCreate.mockReset()
    mCreate.mockResolvedValue({ scheduled: false, task_count: 1 })
  })

  it('立即下发不收描述：描述框不出现，提交体不带描述', async () => {
    const user = userEvent.setup()
    renderDialog()
    expect(screen.queryByLabelText('模板描述')).toBeNull()
    await user.type(screen.getByRole('textbox', { name: '任务目标' }), '扫描 http://a.test')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(mCreate).toHaveBeenCalled())
    expect(mCreate.mock.calls[0][0]).not.toHaveProperty('description')
    expect(mCreate.mock.calls[0][0].cron).toBe('')
  })

  it('定时执行时才出现模板描述，并随模板提交', async () => {
    const user = userEvent.setup()
    mCreate.mockResolvedValue({ scheduled: true, task_count: 1, next_run_at: '2026-11-23T01:00:00Z' })
    renderDialog()
    await user.click(screen.getByRole('checkbox', { name: '定时执行' }))
    await user.type(screen.getByLabelText('模板描述'), '每周复扫')
    await user.type(screen.getByRole('textbox', { name: '任务目标' }), '扫描 http://a.test')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(mCreate).toHaveBeenCalled())
    expect(mCreate.mock.calls[0][0]).toMatchObject({ description: '每周复扫', cron: '0 9 * * 1' })
  })
})
