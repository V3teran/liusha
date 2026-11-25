import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SeverityBadges, SeverityTally } from './SeverityCounts'

// 列表里是五个数的紧凑写法：固定顺序、各档用自己的颜色、0 也占位。顺序本身没有写出来，
// 所以格子的悬停提示与读屏要给出带档名的说法。
describe('SeverityTally', () => {
  it('五个数按 严重 / 高危 / 中危 / 低危 / 信息 排列，用斜线隔开', () => {
    render(<SeverityTally counts={{ critical: 1, high: 3, medium: 2, low: 1, info: 1 }} />)
    expect(screen.getByTitle('严重 1 / 高危 3 / 中危 2 / 低危 1 / 信息 1').textContent).toBe(
      '1/3/2/1/1（严重 1 / 高危 3 / 中危 2 / 低危 1 / 信息 1）',
    )
  })

  it('有发现的那几档着色，为 0 的档照样占位、不着色', () => {
    render(<SeverityTally counts={{ high: 2, info: 1 }} />)
    const colored = screen.getByText('2')
    expect(colored.getAttribute('style')).toContain('85%')
    expect(colored.getAttribute('style')).toContain('--sev-high')
    expect(screen.getByText('1').getAttribute('style')).toContain('--sev-info')
    const zero = screen.getAllByText('0')
    expect(zero).toHaveLength(3)
    for (const z of zero) expect(z.className).toContain('text-text-tertiary')
  })

  it('没有发现时是 0/0/0/0/0', () => {
    render(<SeverityTally counts={{}} />)
    expect(screen.getByTitle('严重 0 / 高危 0 / 中危 0 / 低危 0 / 信息 0')).toBeTruthy()
  })
})

// 任务详情里的气泡：一档一枚徽章，与发现台账同一套色；为 0 的档不列。
describe('SeverityBadges', () => {
  it('各档一枚徽章，按严重程度从高到低', () => {
    render(<SeverityBadges counts={{ low: 7, critical: 1, medium: 12 }} />)
    expect(screen.getByText('严重 1').getAttribute('style')).toContain('--sev-critical')
    expect(screen.getByText('中危 12').getAttribute('style')).toContain('--sev-medium')
    expect(screen.getByText('低危 7').getAttribute('style')).toContain('--sev-low')
    expect(screen.queryByText(/高危|信息/)).toBeNull()
  })

  it('一个发现都没有时留「—」', () => {
    render(<SeverityBadges counts={{ high: 0 }} />)
    expect(screen.getByText('—')).toBeTruthy()
  })
})
