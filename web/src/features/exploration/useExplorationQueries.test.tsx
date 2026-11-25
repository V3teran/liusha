import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useTaskGraph } from './useExplorationQueries'
import { getTaskGraph, type ExplorationGraph } from '@/api/explorationgraph'

vi.mock('@/api/explorationgraph', () => ({
  getTaskGraph: vi.fn(async () => GRAPH),
  getTaskVerifications: vi.fn(async () => ({ verifications: [] })),
}))

// 一张静止的图：动作都排完了、还没有被认领，图上没有 running 节点。
const GRAPH: ExplorationGraph = {
  nodes: [
    {
      id: 'a-1',
      task_id: 't-1',
      kind: 'action',
      state: 'open',
      content: { instruction: '探测入口' },
      source_type: 'planner',
      created_at: '',
      updated_at: '',
    },
  ],
  edges: [],
}

function Probe({ live }: { live: boolean }) {
  useTaskGraph('t-1', live)
  return null
}

// 是否在跑以任务行为准，图自己说了不算：动作刚排出、还没被认领时图上没有 running 节点，
// 照图判断会在这时停掉轮询，此后的执行（认领、观察落图）就再也看不到了。
describe('useTaskGraph 的轮询', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(getTaskGraph).mockClear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  function mount(live: boolean) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <Probe live={live} />
      </QueryClientProvider>,
    )
  }

  // 假定时器下 waitFor 自己的定时器也走不进：用推进 0ms 把挂载后的首取冲出来
  const settle = () => vi.advanceTimersByTimeAsync(0)

  it('任务在跑：图上没有 running 节点也每 10 秒重取', async () => {
    mount(true)
    await settle()
    await settle()
    await settle()
    expect(getTaskGraph).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(getTaskGraph).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(getTaskGraph).toHaveBeenCalledTimes(3)
  })

  it('任务已结束或暂停：取一次即静止', async () => {
    mount(false)
    await settle()
    await settle()
    await settle()
    expect(getTaskGraph).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(getTaskGraph).toHaveBeenCalledTimes(1)
  })
})
