import { beforeAll, describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { LogsPage } from '@/pages/LogsPage'
import { listLogProcesses, readLogs } from '@/api/logs'
import type { LogEntry, LogFilters, LogProcessInfo, LogReadResponse } from '@/api/types'

vi.mock('@/api/logs', () => ({
  listLogProcesses: vi.fn(),
  readLogs: vi.fn(),
}))

const mockedListLogProcesses = listLogProcesses as unknown as ReturnType<typeof vi.fn>
const mockedReadLogs = readLogs as unknown as ReturnType<typeof vi.fn>

function makeEntry(overrides: Partial<LogEntry> = {}): LogEntry {
  return {
    offset: 100,
    time: '2026-01-01T10:00:01.500Z',
    level: 'info',
    service: 'api',
    instance: '72661@hohehoe.local',
    caller: 'httpapi/a.go:1',
    message: 'GET /api/tasks 200',
    ...overrides,
  }
}

function makeProcess(overrides: Partial<LogProcessInfo> = {}): LogProcessInfo {
  return {
    name: 'api',
    exists: true,
    size_bytes: 4096,
    mod_time: '2026-01-01T10:00:02Z',
    instances: ['72661@hohehoe.local'],
    ...overrides,
  }
}

function makeResponse(entries: LogEntry[]): LogReadResponse {
  return { entries, cursors: { api: 4096 }, reset: [], truncated: [] }
}

// React Query 需要 Provider；筛选态走 URL 需要 Router（对齐 FindingsPage 测试模式）。
function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/logs']}>
        <LogsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('LogsPage', () => {
  beforeAll(() => {
    // jsdom 未实现 Element.scrollTo（跟随滚底的 effect 会用到）
    Element.prototype.scrollTo = () => {}
  })

  beforeEach(() => {
    mockedListLogProcesses.mockReset()
    mockedReadLogs.mockReset()
    mockedListLogProcesses.mockResolvedValue({
      processes: [makeProcess(), makeProcess({ name: 'runner', exists: false, instances: [] }), makeProcess({ name: 'proxy' })],
    })
  })

  it('冷读渲染日志行（时间/级别/进程·实例/消息/caller）', async () => {
    mockedReadLogs.mockResolvedValue(makeResponse([makeEntry()]))
    renderPage()
    expect(await screen.findByText('GET /api/tasks 200')).toBeTruthy()
    expect(screen.getByTitle('72661@hohehoe.local')).toBeTruthy()
    expect(screen.getByText('httpapi/a.go:1')).toBeTruthy()
    expect(screen.getByText(/共 1 条/)).toBeTruthy()
  })

  it('error 行：摘要合成「message: error」，展开显示 error 值与业务字段', async () => {
    const user = userEvent.setup()
    mockedReadLogs.mockResolvedValue(
      makeResponse([
        makeEntry({
          level: 'error',
          message: 'list conversations failed',
          error: 'connect refused',
          fields: { task_id: 't-1' },
        }),
      ]),
    )
    renderPage()
    // 行内摘要 = message 前缀 + 错误文本，不再是光秃秃的 message
    const row = await screen.findByText('list conversations failed: connect refused')
    await user.click(row)
    expect(await screen.findByText('error: connect refused')).toBeTruthy()
    expect(screen.getByText('task_id: t-1')).toBeTruthy()
    // 实例与调用位置在窄屏行内隐藏，展开详情必须带上
    expect(screen.getByText('instance: 72661@hohehoe.local')).toBeTruthy()
    expect(screen.getByText('caller: httpapi/a.go:1')).toBeTruthy()
    // 再点收起
    await user.click(row)
    await waitFor(() => expect(screen.queryByText('error: connect refused')).toBeNull())
  })

  it('访问日志形状合成「方法 路径 状态 · 耗时 · 客户端」摘要', async () => {
    mockedReadLogs.mockResolvedValue(
      makeResponse([
        makeEntry({
          message: 'request',
          fields: { method: 'GET', path: '/api/logs', status: 200, latency_ms: 0, client: '::1' },
        }),
      ]),
    )
    renderPage()
    // "request" 占位符还原为可读的访问摘要
    expect(await screen.findByText('GET /api/logs 200 · 0ms · ::1')).toBeTruthy()
  })

  it('无错误字段时业务字段以 k=v 拼接为摘要', async () => {
    mockedReadLogs.mockResolvedValue(
      makeResponse([makeEntry({ message: 'action completed', fields: { task_id: 't-1', action_id: 'a-9' } })]),
    )
    renderPage()
    expect(await screen.findByText('task_id=t-1 action_id=a-9')).toBeTruthy()
  })

  it('进程筛选变化触发带 process 参数的重取', async () => {
    const user = userEvent.setup()
    mockedReadLogs.mockResolvedValue(makeResponse([makeEntry()]))
    renderPage()
    await waitFor(() => expect(mockedReadLogs).toHaveBeenCalled())
    await user.selectOptions(screen.getByLabelText('按进程筛选'), 'runner')
    await waitFor(() => {
      const filters = mockedReadLogs.mock.calls.at(-1)![0] as LogFilters
      expect(filters.process).toBe('runner')
      expect(filters.instance).toBe('') // 切进程时实例联动清空
    })
  })

  it('级别筛选传 level 参数', async () => {
    const user = userEvent.setup()
    mockedReadLogs.mockResolvedValue(makeResponse([makeEntry()]))
    renderPage()
    await waitFor(() => expect(mockedReadLogs).toHaveBeenCalled())
    await user.selectOptions(screen.getByLabelText('按级别筛选'), 'error')
    await waitFor(() => {
      expect((mockedReadLogs.mock.calls.at(-1)![0] as LogFilters).level).toBe('error')
    })
  })
})
