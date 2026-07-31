import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useTheme } from './useTheme'

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })
  afterEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
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
})
