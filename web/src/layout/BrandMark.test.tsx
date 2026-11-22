import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BrandMark } from './Sidebar'

// 品牌徽记在两个主题下用同一枚应用图标（黑剪影 + 白容器）：与登录页、favicon 完全一致，
// 资产自带浅底圆角容器与发丝描边，深色背景上也能与背景分离；外面不套容器。
describe('BrandMark', () => {
  it('浅色主题用应用图标，不外套容器', () => {
    render(<BrandMark className="h-7 w-7" />)
    const img = screen.getByRole('img', { name: 'liusha' })
    expect(img.getAttribute('src')).toBe('/brand/app-icon.svg')
    expect(img.className).toContain('h-7 w-7')
    expect(img.parentElement?.className ?? '').not.toMatch(/ring-|bg-gradient/)
  })

  it('深色主题用同一枚应用图标', () => {
    render(<BrandMark />)
    const img = screen.getByRole('img', { name: 'liusha' })
    expect(img.getAttribute('src')).toBe('/brand/app-icon.svg')
    expect(img.className).not.toContain('h-2/3')
  })
})
