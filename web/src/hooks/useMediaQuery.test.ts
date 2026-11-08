import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useMediaQuery } from './useMediaQuery'

type Listener = (e: { matches: boolean }) => void

function mockMatchMedia(initial: boolean) {
  const listeners = new Set<Listener>()
  const mql = {
    matches: initial,
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
  }
  vi.stubGlobal('matchMedia', vi.fn(() => mql))
  return {
    set(matches: boolean) {
      mql.matches = matches
      listeners.forEach((l) => l({ matches }))
    },
    listenerCount: () => listeners.size,
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useMediaQuery', () => {
  it('首帧即返回当前匹配结果（无闪烁）', () => {
    mockMatchMedia(true)
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(true)
  })

  it('视口跨越断点时更新', () => {
    const mq = mockMatchMedia(false)
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(false)
    act(() => mq.set(true))
    expect(result.current).toBe(true)
  })

  it('卸载时移除监听', () => {
    const mq = mockMatchMedia(false)
    const { unmount } = renderHook(() => useMediaQuery('(min-width: 768px)'))
    expect(mq.listenerCount()).toBe(1)
    unmount()
    expect(mq.listenerCount()).toBe(0)
  })
})
