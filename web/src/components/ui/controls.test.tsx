import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
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
  it('渲染起止两个带无障碍标签的 datetime-local 输入', () => {
    render(<DateTimeRange start="" end="" onChange={() => {}} startLabel="创建时间起" endLabel="创建时间止" />)
    expect(screen.getByLabelText('创建时间起')).toHaveAttribute('type', 'datetime-local')
    expect(screen.getByLabelText('创建时间止')).toHaveAttribute('type', 'datetime-local')
  })

  it('改起点时保留终点，改终点时保留起点', () => {
    const onChange = vi.fn()
    render(
      <DateTimeRange
        start="2026-11-01T00:00"
        end="2026-11-02T00:00"
        onChange={onChange}
        startLabel="起"
        endLabel="止"
      />,
    )
    fireEvent.change(screen.getByLabelText('起'), { target: { value: '2026-10-31T08:00' } })
    expect(onChange).toHaveBeenLastCalledWith({ start: '2026-10-31T08:00', end: '2026-11-02T00:00' })
    fireEvent.change(screen.getByLabelText('止'), { target: { value: '2026-11-03T08:00' } })
    expect(onChange).toHaveBeenLastCalledWith({ start: '2026-11-01T00:00', end: '2026-11-03T08:00' })
  })
})
