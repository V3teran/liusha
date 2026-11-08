import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { FilterBar } from './FilterBar'

describe('FilterBar', () => {
  it('渲染筛选控件与右侧附属区（分页）', () => {
    render(
      <FilterBar trailing={<span>分页器</span>}>
        <input aria-label="搜索" />
      </FilterBar>,
    )
    expect(screen.getByLabelText('搜索')).toBeInTheDocument()
    expect(screen.getByText('分页器')).toBeInTheDocument()
  })

  it('无生效筛选时不显示「清空筛选」', () => {
    render(
      <FilterBar onReset={() => {}} canReset={false}>
        <input aria-label="搜索" />
      </FilterBar>,
    )
    expect(screen.queryByRole('button', { name: '清空筛选' })).toBeNull()
  })

  it('有生效筛选时显示「清空筛选」，点击回调 onReset', () => {
    const onReset = vi.fn()
    render(
      <FilterBar onReset={onReset} canReset>
        <input aria-label="搜索" />
      </FilterBar>,
    )
    fireEvent.click(screen.getByRole('button', { name: '清空筛选' }))
    expect(onReset).toHaveBeenCalledTimes(1)
  })

  it('以 search 地标暴露给辅助技术', () => {
    render(
      <FilterBar label="任务筛选">
        <input aria-label="搜索" />
      </FilterBar>,
    )
    expect(screen.getByRole('search', { name: '任务筛选' })).toBeInTheDocument()
  })
})
