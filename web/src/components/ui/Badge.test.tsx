import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './Badge'

describe('Badge', () => {
  it('软底与 --surface 混成不透明色（不透出容器底色，文字对比度不随所在行变化）', () => {
    render(<Badge color="var(--cat-sky)">执行</Badge>)
    const el = screen.getByText('执行')
    expect(el.style.background).toContain('var(--surface)')
    expect(el.style.background).not.toContain('transparent')
  })

  it('outline 变体不带内联配色', () => {
    render(
      <Badge variant="outline" color="var(--cat-sky)">
        内置
      </Badge>,
    )
    expect(screen.getByText('内置').getAttribute('style')).toBeNull()
  })

  // 进行中状态只让色点呼吸：整枚徽章（含文字）做透明度脉冲会让文字周期性跌到 50% 不透明，
  // 对比度低于 WCAG AA；脉冲须受「减弱动效」偏好约束。
  it('pulse 只作用于色点，文字不透明度恒定', () => {
    render(
      <Badge pulse color="var(--status-info)">
        运行中
      </Badge>,
    )
    const root = screen.getByText('运行中')
    expect(root.className).not.toMatch(/animate-pulse/)
    expect(root.querySelector('span')?.className).toMatch(/motion-safe:animate-pulse/)
  })

  it('不传 pulse 时色点静态', () => {
    render(<Badge color="var(--status-info)">已完成</Badge>)
    expect(screen.getByText('已完成').querySelector('span')?.className).not.toMatch(/animate-pulse/)
  })
})
