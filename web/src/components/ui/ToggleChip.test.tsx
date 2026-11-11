import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ToggleChip } from './ToggleChip'

describe('ToggleChip', () => {
  it('pressed 同步为 aria-pressed，点击回调', () => {
    const onClick = vi.fn()
    const { rerender } = render(
      <ToggleChip pressed={false} onClick={onClick}>
        执行者
      </ToggleChip>,
    )
    const chip = screen.getByRole('button', { name: '执行者' })
    expect(chip).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(chip)
    expect(onClick).toHaveBeenCalledTimes(1)
    rerender(
      <ToggleChip pressed onClick={onClick}>
        执行者
      </ToggleChip>,
    )
    expect(chip).toHaveAttribute('aria-pressed', 'true')
  })

  it('选中态不只靠颜色：强调描边 + 淡底', () => {
    render(<ToggleChip pressed>规划者</ToggleChip>)
    const cls = screen.getByRole('button').className.split(/\s+/)
    expect(cls).toContain('border-accent')
    expect(cls).toContain('bg-accent-soft')
  })

  it('busy 时禁用并标记 aria-busy，点击不回调', () => {
    const onClick = vi.fn()
    render(
      <ToggleChip pressed={false} busy onClick={onClick}>
        a
      </ToggleChip>,
    )
    const chip = screen.getByRole('button')
    expect(chip).toBeDisabled()
    expect(chip).toHaveAttribute('aria-busy', 'true')
    fireEvent.click(chip)
    expect(onClick).not.toHaveBeenCalled()
  })
})
