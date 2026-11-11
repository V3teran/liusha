import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Select } from './Select'
import { DateTimeRange } from './DateTimeRange'

describe('Select', () => {
  it('渲染原生 select，透传 value / onChange / aria-label 与选项', () => {
    const onChange = vi.fn()
    render(
      <Select aria-label="按状态筛选" value="b" onChange={onChange}>
        <option value="a">甲</option>
        <option value="b">乙</option>
      </Select>,
    )
    const el = screen.getByRole('combobox', { name: '按状态筛选' }) as HTMLSelectElement
    expect(el.value).toBe('b')
    fireEvent.change(el, { target: { value: 'a' } })
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('size 决定控件高度档位（md 默认 h-9，sm 为 h-8）', () => {
    const { rerender } = render(
      <Select aria-label="s">
        <option>1</option>
      </Select>,
    )
    expect(screen.getByRole('combobox').className).toContain('h-9')
    rerender(
      <Select aria-label="s" size="sm">
        <option>1</option>
      </Select>,
    )
    expect(screen.getByRole('combobox').className).toContain('h-8')
  })

  it('className 作用于外层容器（宽度由调用方决定），select 撑满容器', () => {
    const { container } = render(
      <Select aria-label="s" className="w-40">
        <option>1</option>
      </Select>,
    )
    expect((container.firstChild as HTMLElement).className).toContain('w-40')
    expect(screen.getByRole('combobox').className).toContain('w-full')
  })

  it('disabled 透传到原生控件', () => {
    render(
      <Select aria-label="s" disabled>
        <option>1</option>
      </Select>,
    )
    expect(screen.getByRole('combobox')).toBeDisabled()
  })
})

describe('DateTimeRange', () => {
  afterEach(() => vi.useRealTimers())

  it('收起态是一个紧凑触发钮：未设边界时显示「全部时间」', () => {
    render(<DateTimeRange label="创建时间" start="" end="" onChange={() => {}} />)
    const trigger = screen.getByRole('button', { name: /创建时间/ })
    expect(trigger).toHaveTextContent('全部时间')
    expect(screen.queryByLabelText('创建时间起')).toBeNull()
  })

  it('设了边界时触发钮显示区间摘要，开放端写作「最早 / 现在」', () => {
    const { rerender } = render(
      <DateTimeRange label="创建时间" start="2026-11-01T08:05" end="" onChange={() => {}} />,
    )
    expect(screen.getByRole('button', { name: /创建时间/ })).toHaveTextContent('11-01 08:05 → 现在')
    rerender(<DateTimeRange label="创建时间" start="" end="2026-11-02T09:00" onChange={() => {}} />)
    expect(screen.getByRole('button', { name: /创建时间/ })).toHaveTextContent('最早 → 11-02 09:00')
  })

  it('展开后渲染起止两个带无障碍标签的 datetime-local 输入', async () => {
    render(<DateTimeRange label="创建时间" start="" end="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: /创建时间/ }))
    expect(screen.getByLabelText('创建时间起')).toHaveAttribute('type', 'datetime-local')
    expect(screen.getByLabelText('创建时间止')).toHaveAttribute('type', 'datetime-local')
  })

  it('改起点时保留终点，改终点时保留起点', async () => {
    const onChange = vi.fn()
    render(
      <DateTimeRange label="区间" start="2026-11-01T00:00" end="2026-11-02T00:00" onChange={onChange} />,
    )
    await userEvent.click(screen.getByRole('button', { name: /区间/ }))
    fireEvent.change(screen.getByLabelText('区间起'), { target: { value: '2026-10-31T08:00' } })
    expect(onChange).toHaveBeenLastCalledWith({ start: '2026-10-31T08:00', end: '2026-11-02T00:00' })
    fireEvent.change(screen.getByLabelText('区间止'), { target: { value: '2026-11-03T08:00' } })
    expect(onChange).toHaveBeenLastCalledWith({ start: '2026-11-01T00:00', end: '2026-11-03T08:00' })
  })

  it('快捷区间：起点 = 现在往前推，终点不限', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true, now: new Date(2026, 10, 10, 12, 30) })
    const onChange = vi.fn()
    render(<DateTimeRange label="区间" start="" end="" onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: /区间/ }))
    await userEvent.click(screen.getByRole('button', { name: '最近 1 小时' }))
    expect(onChange).toHaveBeenLastCalledWith({ start: '2026-11-10T11:30', end: '' })
  })

  it('「清除」同时清空起止', async () => {
    const onChange = vi.fn()
    render(<DateTimeRange label="区间" start="2026-11-01T00:00" end="2026-11-02T00:00" onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: /区间/ }))
    await userEvent.click(screen.getByRole('button', { name: '清除' }))
    expect(onChange).toHaveBeenLastCalledWith({ start: '', end: '' })
  })
})
