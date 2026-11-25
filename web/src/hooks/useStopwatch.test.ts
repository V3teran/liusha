import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStopwatch } from './useStopwatch'

// 对话顶栏的耗时原先直接显示每 10 秒轮询一次的服务端值，一格一格跳；现在以服务端的值为基准本地走秒。
describe('useStopwatch', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('在跑：从服务端的值起逐秒往下走', () => {
    const { result } = renderHook(() => useStopwatch(5000, true))
    expect(result.current).toBe(5000)
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(result.current).toBe(8000)
  })

  it('服务端的新值与本地估算差不到一秒：当作在路上的延迟，不重设基准（显示不往回跳）', () => {
    const { result, rerender } = renderHook(({ ms }: { ms: number }) => useStopwatch(ms, true), {
      initialProps: { ms: 5000 },
    })
    act(() => {
      vi.advanceTimersByTime(10000)
    })
    expect(result.current).toBe(15000)
    // 轮询回来了：服务端算出来是 14.9 秒
    rerender({ ms: 14900 })
    expect(result.current).toBe(15000)
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(result.current).toBe(16000)
  })

  it('服务端的新值差了一秒以上：以它重新对表（续接后扣掉中间的停顿）', () => {
    const { result, rerender } = renderHook(({ ms, running }: { ms: number; running: boolean }) => useStopwatch(ms, running), {
      initialProps: { ms: 5000, running: true },
    })
    act(() => {
      vi.advanceTimersByTime(60000)
    })
    expect(result.current).toBe(65000)
    rerender({ ms: 8000, running: true })
    expect(result.current).toBe(8000)
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(result.current).toBe(9000)
  })

  it('不在跑：原样显示服务端的值，不走', () => {
    const { result } = renderHook(() => useStopwatch(4200, false))
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(result.current).toBe(4200)
  })

  it('还没有服务端的值时没有可显示的', () => {
    const { result } = renderHook(() => useStopwatch(undefined, true))
    expect(result.current).toBeUndefined()
  })
})
