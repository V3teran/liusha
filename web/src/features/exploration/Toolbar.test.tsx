import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
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
        stats={{ total: 0, edgeCount: 0, byKind: {}, byState: {} }}
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

// 状态筛选只列这张图里实际有的状态，带上节点数；图上没有的状态不占位。
describe('探索图 Toolbar · 状态筛选', () => {
  it('只列图里有的状态，按生命周期的先后，带节点数', () => {
    setup({ stats: { total: 12, edgeCount: 11, byKind: {}, byState: { completed: 10, running: 1, open: 1 } } })
    const select = screen.getByRole('combobox', { name: '按状态筛选节点' })
    const options = within(select).getAllByRole('option').map((o) => o.textContent)
    expect(options).toEqual(['全部状态', '待处理（1）', '执行中（1）', '已完成（10）'])
  })

  it('当前选中的状态即使图上已经没有也留着，下拉框显示的与生效的筛选对得上', () => {
    setup({ filterState: 'failed', stats: { total: 3, edgeCount: 0, byKind: {}, byState: { completed: 3 } } })
    const select = screen.getByRole('combobox', { name: '按状态筛选节点' }) as HTMLSelectElement
    expect(select.value).toBe('failed')
    expect(within(select).getByRole('option', { name: '失败（0）' })).toBeTruthy()
  })

  it('选一个状态即交出筛选', async () => {
    const onFilterState = vi.fn()
    const user = setup({ onFilterState, stats: { total: 2, edgeCount: 0, byKind: {}, byState: { running: 1, aborted: 1 } } })
    await user.selectOptions(screen.getByRole('combobox', { name: '按状态筛选节点' }), 'aborted')
    expect(onFilterState).toHaveBeenCalledWith('aborted')
  })
})
