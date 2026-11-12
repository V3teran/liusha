import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

// 首次访问（未主动选过）跟随系统深浅偏好；主动切换后记住选择、不再跟随系统。
describe('useTheme 跟随系统偏好', () => {
  let systemLight = false
  const changeListeners = new Set<() => void>()
  beforeEach(() => {
    localStorage.clear()
    changeListeners.clear()
    document.documentElement.classList.remove('dark', 'theme-switching')
    vi.stubGlobal('matchMedia', (query: string) => ({
      get matches() {
        return query === '(prefers-color-scheme: light)' && systemLight
      },
      media: query,
      addEventListener: (_: string, fn: () => void) => changeListeners.add(fn),
      removeEventListener: (_: string, fn: () => void) => changeListeners.delete(fn),
    }))
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
    document.documentElement.classList.remove('dark', 'theme-switching')
  })

  it('未选过：系统浅色 → 浅色', () => {
    systemLight = true
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('未选过：系统深色 → 深色', () => {
    systemLight = false
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
  })

  it('已选过的主题优先于系统偏好', () => {
    systemLight = true
    localStorage.setItem('liusha-theme', 'dark')
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
  })

  it('未选过时随系统切换实时更新；主动切换后不再跟随', () => {
    systemLight = false
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')

    systemLight = true
    act(() => changeListeners.forEach((fn) => fn()))
    expect(result.current.theme).toBe('light')
    expect(localStorage.getItem('liusha-theme')).toBeNull()

    act(() => result.current.toggle())
    expect(result.current.theme).toBe('dark')
    systemLight = false
    act(() => changeListeners.forEach((fn) => fn()))
    systemLight = true
    act(() => changeListeners.forEach((fn) => fn()))
    expect(result.current.theme).toBe('dark')
  })
})

// 首屏内联脚本（index.html）须与 useTheme 同口径：已选优先，否则跟随系统，读不到按深色。
describe('index.html 首屏主题脚本', () => {
  const html = Object.values(
    import.meta.glob('/index.html', { query: '?raw', import: 'default', eager: true }) as Record<string, string>,
  )[0]
  it('未选过时读系统偏好', () => {
    expect(html).toContain("localStorage.getItem('liusha-theme')")
    expect(html).toContain("matchMedia('(prefers-color-scheme: light)')")
  })
})
