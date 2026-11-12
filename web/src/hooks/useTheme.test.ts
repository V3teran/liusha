import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useTheme } from './useTheme'

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark', 'theme-switching')
  })
  afterEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark', 'theme-switching')
  })

  it('默认主题为 dark（无 localStorage 记录时）', () => {
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('从 localStorage 恢复已保存的 light 主题', () => {
    localStorage.setItem('liusha-theme', 'light')
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('toggle 切换主题并持久化到 localStorage', () => {
    const { result } = renderHook(() => useTheme())
    act(() => result.current.toggle())
    expect(result.current.theme).toBe('light')
    expect(localStorage.getItem('liusha-theme')).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    act(() => result.current.toggle())
    expect(result.current.theme).toBe('dark')
    expect(localStorage.getItem('liusha-theme')).toBe('dark')
  })

  it('localStorage 中的非法值回退为 dark', () => {
    localStorage.setItem('liusha-theme', 'garbage')
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
  })

  it('切换瞬间冻结过渡（theme-switching），绘制后解冻——单帧原子重绘防闪烁', async () => {
    const { result } = renderHook(() => useTheme())
    act(() => result.current.toggle())
    // 翻主题的同一步挂上冻结类：过渡动画在切换期间全程禁用。
    expect(document.documentElement.classList.contains('theme-switching')).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    // 双 rAF 后解冻，悬停过渡恢复。
    await waitFor(() =>
      expect(document.documentElement.classList.contains('theme-switching')).toBe(false),
    )
  })

  // 主题是全局状态：各调用点（侧栏切换钮、品牌徽记、启动 splash）必须看到同一个值。
  // 曾经每个调用点各持一份 useState，侧栏里切换主题后品牌徽记仍停在旧主题。
  it('一处切换，其它调用点同步更新', () => {
    const a = renderHook(() => useTheme())
    const b = renderHook(() => useTheme())
    expect(b.result.current.theme).toBe('dark')

    act(() => a.result.current.toggle())
    expect(b.result.current.theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    act(() => b.result.current.toggle())
    expect(a.result.current.theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
