import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useCopyToClipboard } from './useCopyToClipboard'

describe('useCopyToClipboard', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('复制成功后 copiedKey 置为传入 key，1.6s 后自动恢复', async () => {
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy('rid', 'req-123')
    })
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('req-123')
    expect(result.current.copiedKey).toBe('rid')

    act(() => {
      vi.advanceTimersByTime(1600)
    })
    expect(result.current.copiedKey).toBe('')
  })

  it('连续复制不同 key，只有最新的 key 处于已复制态', async () => {
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy('a', 'text-a')
    })
    expect(result.current.copiedKey).toBe('a')

    await act(async () => {
      await result.current.copy('b', 'text-b')
    })
    expect(result.current.copiedKey).toBe('b')
  })

  it('clipboard 不可用时静默失败，不抛错', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    })
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy('x', 'text')
    })
    expect(result.current.copiedKey).toBe('')
  })

  it('卸载时清理挂起的 timer', async () => {
    const { result, unmount } = renderHook(() => useCopyToClipboard())
    await act(async () => {
      await result.current.copy('rid', 'req-123')
    })
    unmount()
    // 卸载后 timer 触发不应抛错（clearTimeout 已执行，advance 应为 no-op）
    expect(() => vi.advanceTimersByTime(2000)).not.toThrow()
  })
})
