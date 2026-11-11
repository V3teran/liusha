import { StrictMode, createElement, type ReactNode } from 'react'
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLogStream } from './useLogStream'
import { readLogs } from '@/api/logs'
import type { LogFilters } from '@/api/types'

vi.mock('@/api/logs', () => ({ readLogs: vi.fn() }))
const mockedRead = vi.mocked(readLogs)

const FILTERS: LogFilters = { process: '', instance: '', level: '', search: '' }
const page = () => ({ entries: [], cursors: { api: 1 }, reset: [], truncated: [] })

describe('useLogStream 轮询', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  // 请求慢于轮询周期时不得并发：并发请求携带同一游标，返回后会重复追加日志。
  it('上一次请求未落定前不发起下一次', async () => {
    let finish!: (v: ReturnType<typeof page>) => void
    mockedRead.mockImplementationOnce(() => new Promise((r) => (finish = r)))
    mockedRead.mockResolvedValue(page() as never)
    renderHook(() => useLogStream(FILTERS, true))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000) // 远超轮询周期，首个请求仍在途
    })
    expect(mockedRead).toHaveBeenCalledTimes(1)

    await act(async () => {
      finish(page())
      await vi.advanceTimersByTimeAsync(3_000)
    })
    expect(mockedRead).toHaveBeenCalledTimes(2)
  })

  // 状态更新函数必须是纯函数：React（开发态 StrictMode、并发渲染重放）可能对同一 prev 调用
  // 两次。曾在更新函数里 push 进上一份 state 的数组，导致同一批日志被追加两次（key 重复告警）。
  it('StrictMode 下增量合并不重复追加', async () => {
    const entry = (offset: number) => ({ service: 'api', offset, level: 'info', time: '', message: `m${offset}` })
    mockedRead.mockResolvedValueOnce({ entries: [entry(1), entry(2)], cursors: { api: 2 }, reset: [], truncated: [] } as never)
    mockedRead.mockResolvedValueOnce({ entries: [entry(3)], cursors: { api: 3 }, reset: [], truncated: [] } as never)
    mockedRead.mockResolvedValue(page() as never)
    const wrapper = ({ children }: { children: ReactNode }) => createElement(StrictMode, null, children)
    const { result } = renderHook(() => useLogStream(FILTERS, true), { wrapper })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_500)
    })
    expect(result.current.byService.get('api')?.map((e) => e.offset)).toEqual([1, 2, 3])
  })
})
