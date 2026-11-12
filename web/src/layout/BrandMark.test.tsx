import { afterEach, describe, expect, it } from 'vitest'
import { act, render, renderHook, screen } from '@testing-library/react'
import { useTheme } from '@/hooks/useTheme'
import { BrandMark } from './Sidebar'

// 浅色主题的品牌徽记必须与登录页同一枚应用图标（黑剪影 + 白容器，资产自带圆角与发丝描边）；
// 深色主题是反白剪影 + accent 着色容器。徽记随页面内的主题切换即时更新。
describe('BrandMark', () => {
  afterEach(() => localStorage.clear())

  it('浅色主题用登录页同款应用图标，不外套容器', () => {
    localStorage.setItem('liusha-theme', 'light')
    render(<BrandMark className="h-7 w-7" />)
    const img = screen.getByRole('img', { name: 'liusha' })
    expect(img.getAttribute('src')).toBe('/brand/app-icon.svg')
    expect(img.className).toContain('h-7 w-7')
    expect(img.parentElement?.className ?? '').not.toMatch(/ring-|bg-gradient/)
  })

  it('深色主题为反白剪影', () => {
    localStorage.setItem('liusha-theme', 'dark')
    render(<BrandMark />)
    expect(screen.getByRole('img', { name: 'liusha' }).getAttribute('src')).toBe('/brand/mark-white.svg')
  })

  it('页面内切换主题，徽记同步换形态', () => {
    localStorage.setItem('liusha-theme', 'light')
    render(<BrandMark />)
    const toggler = renderHook(() => useTheme())
    act(() => toggler.result.current.toggle())
    expect(screen.getByRole('img', { name: 'liusha' }).getAttribute('src')).toBe('/brand/mark-white.svg')
    act(() => toggler.result.current.toggle())
    expect(screen.getByRole('img', { name: 'liusha' }).getAttribute('src')).toBe('/brand/app-icon.svg')
  })
})
