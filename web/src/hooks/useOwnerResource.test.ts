import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useOwnerResource } from './useOwnerResource'

describe('useOwnerResource', () => {
  it('owner 为空时不发起请求', () => {
    const fetchFn = vi.fn()
    const { result } = renderHook(() => useOwnerResource(fetchFn))
    expect(fetchFn).not.toHaveBeenCalled()
    expect(result.current.owner).toBe('')
    expect(result.current.data).toBeNull()
    expect(result.current.loading).toBe(false)
  })

  it('setOwner 触发 fetchFn 并设置返回数据', async () => {
    const fetchFn = vi.fn().mockResolvedValue({ foo: 'bar' })
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(fetchFn).toHaveBeenCalledWith('owner-1')
    expect(result.current.data).toEqual({ foo: 'bar' })
    expect(result.current.error).toBe('')
    expect(result.current.notActive).toBe(false)
  })

  it('loading 状态在请求过程中为 true，请求完成后恢复 false', async () => {
    let resolveFn!: (v: unknown) => void
    const fetchFn = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFn = resolve
        }),
    )
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))
    await waitFor(() => expect(result.current.loading).toBe(true))

    await act(async () => {
      resolveFn({ ok: true })
    })
    expect(result.current.loading).toBe(false)
    expect(result.current.data).toEqual({ ok: true })
  })

  it('抛出 404 错误映射为 notActive，不设置 error', async () => {
    const fetchFn = vi.fn().mockRejectedValue(new Error('请求失败: 404 Not Found'))
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.notActive).toBe(true)
    expect(result.current.error).toBe('')
    expect(result.current.data).toBeNull()
  })

  it('其他错误设置 error，不设置 notActive', async () => {
    const fetchFn = vi.fn().mockRejectedValue(new Error('网络异常'))
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBe('网络异常')
    expect(result.current.notActive).toBe(false)
  })

  it('非 Error 抛出对象时使用默认错误消息', async () => {
    const fetchFn = vi.fn().mockRejectedValue('raw-string-error')
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBe('加载失败')
  })

  it('reload 重新拉取数据', async () => {
    const fetchFn = vi.fn().mockResolvedValueOnce({ v: 1 }).mockResolvedValueOnce({ v: 2 })
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('owner-1'))
    await waitFor(() => expect(result.current.data).toEqual({ v: 1 }))

    await act(async () => {
      await result.current.reload()
    })
    expect(fetchFn).toHaveBeenCalledTimes(2)
    expect(result.current.data).toEqual({ v: 2 })
  })

  it('切换 owner 会用新 owner 重新拉取', async () => {
    const fetchFn = vi.fn().mockImplementation((owner: string) => Promise.resolve({ owner }))
    const { result } = renderHook(() => useOwnerResource(fetchFn))

    act(() => result.current.setOwner('a'))
    await waitFor(() => expect(result.current.data).toEqual({ owner: 'a' }))

    act(() => result.current.setOwner('b'))
    await waitFor(() => expect(result.current.data).toEqual({ owner: 'b' }))
    expect(fetchFn).toHaveBeenCalledTimes(2)
  })
})
