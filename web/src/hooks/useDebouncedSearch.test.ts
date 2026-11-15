import { describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { SEARCH_DEBOUNCE_MS, useDebouncedSearch } from './useDebouncedSearch'

describe('useDebouncedSearch', () => {
  it('输入停顿后才同步，清空立即生效', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ q }) => useDebouncedSearch(q), { initialProps: { q: '' } })
    rerender({ q: 'xs' })
    rerender({ q: 'xss' })
    expect(result.current).toBe('')
    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS))
    expect(result.current).toBe('xss')
    rerender({ q: '' })
    expect(result.current).toBe('')
    vi.useRealTimers()
  })
})
