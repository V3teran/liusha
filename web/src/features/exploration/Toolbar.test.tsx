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

describe('探索图 Toolbar', () => {
  it('探索图属于任务：任务由可搜索的选择框选取（服务端按目标 / 批次名检索）', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Toolbar
          scopeId=""
          onTaskChange={onChange}
          disabled={false}
          refreshing={false}
          onRefresh={vi.fn()}
          showFilters={false}
          filterKind="all"
          onFilterKind={vi.fn()}
          filterState="all"
          onFilterState={vi.fn()}
          directionEdgeCount={0}
          directionOnly={false}
          onDirectionOnly={vi.fn()}
          stats={{ total: 0, edgeCount: 0, byKind: {}, directionEdgeCount: 0 }}
        />
      </QueryClientProvider>,
    )
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

// 滋生与吸收两种边在整图里稀少：有这种边时给一个开关，把图收缩到它们及两端的节点。
describe('探索图 Toolbar · 只看方向关系', () => {
  function setup(over: Partial<Parameters<typeof Toolbar>[0]> = {}) {
    const user = userEvent.setup()
    const onDirectionOnly = vi.fn()
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
          directionEdgeCount={23}
          directionOnly={false}
          onDirectionOnly={onDirectionOnly}
          stats={{ total: 487, edgeCount: 499, byKind: {}, directionEdgeCount: 23 }}
          {...over}
        />
      </QueryClientProvider>,
    )
    return { user, onDirectionOnly }
  }

  it('有滋生或吸收的边时出现开关，点它交出取舍', async () => {
    const { user, onDirectionOnly } = setup()
    await user.click(screen.getByRole('button', { name: '滋生 · 吸收' }))
    expect(onDirectionOnly).toHaveBeenCalledWith(true)
  })

  it('开着时标记为按下，「清空筛选」把它一并复位', async () => {
    const onFilterKind = vi.fn()
    const onFilterState = vi.fn()
    const { user, onDirectionOnly } = setup({ directionOnly: true, onFilterKind, onFilterState })
    expect(screen.getByRole('button', { name: '滋生 · 吸收' }).getAttribute('aria-pressed')).toBe('true')
    await user.click(screen.getByRole('button', { name: '清空筛选' }))
    expect(onDirectionOnly).toHaveBeenCalledWith(false)
    expect(onFilterKind).toHaveBeenCalledWith('all')
    expect(onFilterState).toHaveBeenCalledWith('all')
  })

  it('图上没有这两种边时不出现开关', () => {
    setup({ directionEdgeCount: 0, stats: { total: 1, edgeCount: 0, byKind: {}, directionEdgeCount: 0 } })
    expect(screen.queryByRole('button', { name: '滋生 · 吸收' })).toBeNull()
  })
})
