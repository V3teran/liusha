import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { OwnerPicker } from './OwnerPicker'
import { listTasks } from '@/api/client'
import type { OwnerSummary } from '@/api/types'

vi.mock('@/api/client', () => ({
  listTasks: vi.fn(),
}))

const mockedListTasks = listTasks as unknown as ReturnType<typeof vi.fn>

function makeTask(overrides: Partial<OwnerSummary> = {}): OwnerSummary {
  return {
    id: 'owner-aaaaaaaa-1111',
    scope: '{}',
    status: 'running',
    scenario_id: 'web-pentest-killchain',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('OwnerPicker', () => {
  beforeEach(() => {
    mockedListTasks.mockReset()
  })

  it('挂载时加载任务列表并渲染选项', async () => {
    const tasks = [makeTask({ id: 'aaaaaaaa-1111' }), makeTask({ id: 'bbbbbbbb-2222' })]
    mockedListTasks.mockResolvedValue(tasks)
    const onChange = vi.fn()

    render(<OwnerPicker value="" onChange={onChange} />)

    await waitFor(() => expect(mockedListTasks).toHaveBeenCalledWith(50))
    expect(await screen.findByText(/aaaaaaaa/)).toBeTruthy()
    expect(screen.getByText(/bbbbbbbb/)).toBeTruthy()
  })

  it('未选中值时自动选中第一个 owner', async () => {
    const tasks = [makeTask({ id: 'aaaaaaaa-1111' }), makeTask({ id: 'bbbbbbbb-2222' })]
    mockedListTasks.mockResolvedValue(tasks)
    const onChange = vi.fn()

    render(<OwnerPicker value="" onChange={onChange} />)

    await waitFor(() => expect(onChange).toHaveBeenCalledWith('aaaaaaaa-1111'))
  })

  it('已有选中值时不会覆盖', async () => {
    const tasks = [makeTask({ id: 'aaaaaaaa-1111' }), makeTask({ id: 'bbbbbbbb-2222' })]
    mockedListTasks.mockResolvedValue(tasks)
    const onChange = vi.fn()

    render(<OwnerPicker value="bbbbbbbb-2222" onChange={onChange} />)

    await waitFor(() => expect(mockedListTasks).toHaveBeenCalled())
    expect(onChange).not.toHaveBeenCalled()
  })

  it('选项标签展示场景 code', async () => {
    const tasks = [makeTask({ id: 'aaaaaaaa-1111', scenario_id: 'api-pentest' })]
    mockedListTasks.mockResolvedValue(tasks)
    const onChange = vi.fn()

    render(<OwnerPicker value="" onChange={onChange} />)

    expect(await screen.findByText(/api-pentest/)).toBeTruthy()
  })

  it('点击刷新按钮重新拉取列表', async () => {
    const user = userEvent.setup()
    mockedListTasks.mockResolvedValue([makeTask({ id: 'aaaaaaaa-1111' })])
    const onChange = vi.fn()

    render(<OwnerPicker value="aaaaaaaa-1111" onChange={onChange} />)
    await waitFor(() => expect(mockedListTasks).toHaveBeenCalledTimes(1))

    await user.click(screen.getByTitle('刷新对话列表'))
    await waitFor(() => expect(mockedListTasks).toHaveBeenCalledTimes(2))
  })

  it('加载失败时显示错误信息', async () => {
    mockedListTasks.mockRejectedValue(new Error('网络错误'))
    const onChange = vi.fn()

    render(<OwnerPicker value="" onChange={onChange} />)

    expect(await screen.findByText('网络错误')).toBeTruthy()
  })

  it('非 Error 抛出对象时显示默认错误消息', async () => {
    mockedListTasks.mockRejectedValue('boom')
    const onChange = vi.fn()

    render(<OwnerPicker value="" onChange={onChange} />)

    expect(await screen.findByText('加载对话失败')).toBeTruthy()
  })
})
