import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Toolbar } from './Toolbar'
import { listTasks } from '@/api/tasks'

vi.mock('@/api/tasks', () => ({
  listTasks: vi.fn(async (_page: number, _size: number, filters?: { search?: string }) => ({
    items: filters?.search
      ? [{ id: 't-9', goal: '测试商城越权', status: 'active', source: 'direct', created_at: '2026-11-14T08:30:00' }]
      : [],
    total: filters?.search ? 1 : 0,
    page: 1,
    size: 20,
  })),
}))

function setup(over: Partial<Parameters<typeof Toolbar>[0]> = {}) {
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Toolbar
        scopeId=""
        onTaskChange={vi.fn()}
        disabled={false}
        refreshing={false}
        onRefresh={vi.fn()}
        showFilters
        filterKind="all"
        onFilterKind={vi.fn()}
        filterState="all"
        onFilterState={vi.fn()}
        stats={{ total: 0, edgeCount: 0, byKind: {} }}
        {...over}
      />
    </QueryClientProvider>,
  )
  return user
}

describe('探索图 Toolbar', () => {
  it('探索图属于任务：任务由可搜索的选择框选取（服务端按目标 / 批次名检索）', async () => {
    const onChange = vi.fn()
    const user = setup({ showFilters: false, onTaskChange: onChange })
    await user.click(screen.getByRole('button', { name: '选择任务：选择任务' }))
    await user.type(screen.getByRole('combobox', { name: '搜索任务目标、批次名' }), '商城')
    await waitFor(() => expect(listTasks).toHaveBeenCalledWith(1, 20, { search: '商城' }))
    const option = await screen.findByRole('option', { name: /测试商城越权/ })
    // 次要信息区分目标相同的任务：创建时间与状态
    expect(option.textContent).toContain('11-14 08:30 创建 · 进行中')
    await user.click(option)
    expect(onChange).toHaveBeenCalledWith('t-9')
  })
})
