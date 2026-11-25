import { describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { SEARCH_DEBOUNCE_MS, useDebouncedSearch, useDebouncedSearchState, useSearchDraft } from './useDebouncedSearch'

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

describe('useDebouncedSearchState', () => {
  it('flush 让输入框里的词立即生效；之后再输入，防抖照常工作', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ q }) => useDebouncedSearchState(q), { initialProps: { q: '' } })
    rerender({ q: 'xs' })
    expect(result.current.pending).toBe(true)
    expect(result.current.debounced).toBe('')
    act(() => result.current.flush())
    expect(result.current.debounced).toBe('xs')
    expect(result.current.pending).toBe(false)

    // flush 之后继续输入：新词仍要等防抖，不会被旧的 flush 挡住
    rerender({ q: 'xss' })
    expect(result.current.debounced).toBe('xs')
    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS))
    expect(result.current.debounced).toBe('xss')
    vi.useRealTimers()
  })
})

describe('useSearchDraft', () => {
  function setup(initial = '') {
    vi.useFakeTimers()
    const onChange = vi.fn()
    const hook = renderHook(({ value }) => useSearchDraft(value, onChange), { initialProps: { value: initial } })
    return { onChange, ...hook }
  }
  const type = (hook: { result: { current: { setDraft: (v: string) => void } } }, text: string) =>
    act(() => hook.result.current.setDraft(text))
  const wait = () => act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS))

  it('输入停顿后才把草稿交出去；连续输入只交出最后一版', () => {
    const { onChange, result } = setup()
    type({ result }, 'x')
    type({ result }, 'xs')
    type({ result }, 'xss')
    expect(onChange).not.toHaveBeenCalled()
    wait()
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('xss')
    vi.useRealTimers()
  })

  it('清空立即提交，不等防抖', () => {
    const { onChange, result } = setup('xss')
    type({ result }, '')
    expect(onChange).toHaveBeenCalledWith('')
    vi.useRealTimers()
  })

  it('submit 现在就交出草稿：有未生效的词才交', () => {
    const { onChange, result } = setup()
    act(() => result.current.setDraft('xss'))
    expect(result.current.pending).toBe(true)
    act(() => result.current.submit())
    expect(onChange).toHaveBeenCalledWith('xss')
    // 草稿已被上层接走（value 跟上来）后再提交：没有要交的
    const settled = renderHook(({ value }) => useSearchDraft(value, onChange), { initialProps: { value: 'xss' } })
    expect(settled.result.current.pending).toBe(false)
    act(() => settled.result.current.submit())
    expect(onChange).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })

  it('上层改写词（清空筛选、前进后退）时草稿即时回填', () => {
    const { onChange, result, rerender } = setup('xss')
    rerender({ value: '' })
    expect(result.current.draft).toBe('')
    expect(onChange).not.toHaveBeenCalled()
    vi.useRealTimers()
  })
})
