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
})
