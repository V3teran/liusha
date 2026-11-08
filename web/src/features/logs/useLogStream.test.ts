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
})
