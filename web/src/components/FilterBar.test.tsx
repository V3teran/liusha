import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { FilterBar } from './FilterBar'
import { SearchInput } from './SearchInput'

describe('FilterBar', () => {
  it('渲染筛选控件与右侧附属区（分页）', () => {
    render(
      <FilterBar trailing={<span>分页器</span>} canReset={false} onReset={() => {}}>
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
      <FilterBar label="任务筛选" canReset={false} onReset={() => {}}>
        <input aria-label="搜索" />
      </FilterBar>,
    )
    expect(screen.getByRole('search', { name: '任务筛选' })).toBeInTheDocument()
  })

  // 同一处只有一种清空方式：栏内的搜索框交给「清空筛选」，独立使用的搜索框自带清空钮。
  it('栏内的搜索框不带清空钮，独立的搜索框有内容时带', () => {
    const onChange = vi.fn()
    const { unmount } = render(
      <FilterBar canReset onReset={() => {}}>
        <SearchInput value="甲" onChange={onChange} />
      </FilterBar>,
    )
    expect(screen.queryByRole('button', { name: '清空搜索' })).toBeNull()
    unmount()

    render(<SearchInput value="甲" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: '清空搜索' }))
    expect(onChange).toHaveBeenCalledWith('')
  })
})
