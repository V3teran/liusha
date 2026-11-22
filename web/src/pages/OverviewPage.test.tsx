import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { OverviewPage } from './OverviewPage'
import { getOverview, getOverviewHealth, type OverviewHealth, type OverviewSnapshot } from '@/api/overview'
import { listTasks } from '@/api/tasks'
import { listLogProcesses } from '@/api/logs'
import type { TaskSummary } from '@/api/types'

vi.mock('@/api/overview', async (orig) => ({
  ...(await orig<typeof import('@/api/overview')>()),
  getOverview: vi.fn(),
  getOverviewHealth: vi.fn(),
}))
vi.mock('@/api/tasks', () => ({ listTasks: vi.fn() }))
vi.mock('@/api/logs', () => ({ listLogProcesses: vi.fn() }))

const mocked = <T,>(fn: T) => fn as unknown as ReturnType<typeof vi.fn>

const ZERO = { calls: 0, errors: 0, in_tokens: 0, out_tokens: 0, cached_tokens: 0 }

function days(n: number) {
  return Array.from({ length: n }, (_, i) => `2026-11-${String(8 + i).padStart(2, '0')}`)
}

function snapshot(over: Partial<OverviewSnapshot> = {}): OverviewSnapshot {
  const dates = days(14)
  return {
    generated_at: '2026-11-21T12:00:00Z',
    window: { days: 14, since: '2026-11-08T00:00:00Z' },
    tasks: { running: 1, paused: 0 },
    findings: {
      open: { critical: 0, high: 2, medium: 1, low: 0, info: 0 },
      daily: dates.map((date, i) => ({ date, counts: (i === 13 ? { high: 2, medium: 1 } : {}) as Record<string, number> })),
      classes: [
        { class: 'sql_injection', name: 'SQL 注入', total: 2, open: 2 },
        { class: 'info_disclosure', name: '敏感信息泄露', total: 1, open: 1 },
        { class: 'other', name: '其他', total: 1, open: 0 },
      ],
    },
    usage: {
      daily: dates.map((date, i) => ({ date, ...ZERO, ...(i === 13 ? { calls: 10, in_tokens: 9000, out_tokens: 1000 } : {}) })),
      last_24h: { calls: 10, errors: 1, in_tokens: 9000, out_tokens: 1000, cached_tokens: 4500 },
    },
    targets: [
      {
        host: '192.168.71.36:8099',
        tasks: 2,
        active_tasks: 1,
        open: { high: 2, medium: 1 },
        findings: 3,
        last_activity_at: '2026-11-21T11:00:00Z',
      },
    ],
    ...over,
  }
}

function health(over: Partial<OverviewHealth> = {}): OverviewHealth {
  return {
    runner: {
      state: 'ok',
      instances: [{ host: 'h1', pid: 42, started_at: '2026-11-21T09:00:00Z', concurrency: 10, active: 2, status: 'active' }],
    },
    proxy: { state: 'ok', last_seen_at: '2026-11-21T11:59:00Z', last_request_at: null, requests_last_hour: 42 },
    sandbox: { state: 'ok', reachable: true, tools_installed: 91, tools_total: 91, browser: true },
    postgres: { state: 'ok', version: '17.2', latency_ms: 1.4, connections: 12, max_connections: 100 },
    redis: { state: 'ok', version: '8.0.2', latency_ms: 0.3, used_memory_bytes: 3 * 1024 * 1024, max_memory_bytes: 0 },
    ...over,
  }
}

function task(over: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't1',
    goal: '测试 192.168.71.36:8099 的登录接口',
    target_host: '192.168.71.36:8099',
    status: 'active',
    paused: false,
    assignment_id: 'a1',
    assignment_title: '批次',
    assignment_index: 0,
    assignment_total: 1,
    source: 'direct',
    scheduled: false,
    schedule_id: '',
    cron: '',
    conversation_id: '',
    scenario: 'pentest',
    scenario_name: '渗透测试',
    scenario_deleted: false,
    finding_count: 3,
    step_count: 12,
    tokens_in: 0,
    tokens_out: 0,
    created_at: '2026-11-21T10:00:00Z',
    ended_at: null,
    abort_reason: '',
    ...over,
  }
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <OverviewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mocked(getOverview).mockResolvedValue(snapshot())
  mocked(getOverviewHealth).mockResolvedValue(health())
  mocked(listTasks).mockResolvedValue({ items: [task()], total: 1, page: 1, size: 6 })
  mocked(listLogProcesses).mockResolvedValue({ processes: ['api', 'proxy', 'runner', 'sandbox'].map((name) => ({ name })) })
})

describe('OverviewPage', () => {
  it('按查看者时区取快照，进行中的任务复用任务列表接口', async () => {
    renderPage()
    await screen.findByRole('region', { name: '进行中的任务' })
    expect(mocked(getOverview).mock.calls[0]?.[0]).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone)
    expect(listTasks).toHaveBeenCalledWith(1, 6, { status: 'active' })
  })

  it('组件健康：三个工作进程与两个数据依赖，读数带标签，进程链到自己的日志，数据依赖不链接', async () => {
    mocked(getOverviewHealth).mockResolvedValue(
      health({
        sandbox: { state: 'warn', reachable: true, tools_installed: 80, tools_total: 91, browser: true },
        redis: { state: 'down', version: '', latency_ms: 0, used_memory_bytes: 0, max_memory_bytes: 0 },
      }),
    )
    renderPage()
    const strip = await screen.findByRole('list', { name: '组件健康' })
    expect(within(strip).getAllByRole('listitem')).toHaveLength(5)
    expect(within(strip).queryByText(/^API/)).toBeNull()
    const runner = await within(strip).findByRole('link', { name: /^Runner/ })
    expect(runner).toHaveAttribute('href', '/logs?process=runner')
    expect(runner).toHaveTextContent('执行 2 个任务，并发上限 10')
    const proxy = within(strip).getByRole('link', { name: /^Proxy/ })
    expect(proxy).toHaveAttribute('href', '/logs?process=proxy')
    expect(proxy).toHaveTextContent('近 1 小时 42 个请求')
    const sandbox = within(strip).getByRole('link', { name: /^Sandbox/ })
    expect(sandbox).toHaveAttribute('href', '/logs?process=sandbox')
    expect(sandbox).toHaveTextContent('有隐患')
    const pg = within(strip).getByText(/^PostgreSQL/).closest('li')
    expect(pg).toHaveTextContent('延迟 1 ms，连接 12/100')
    expect(pg).toHaveAttribute('title', 'PostgreSQL 17.2')
    expect(within(pg as HTMLElement).queryByRole('link')).toBeNull()
    const redis = within(strip).getByText(/^Redis/).closest('li')
    expect(redis).toHaveTextContent('不可用')
    expect(redis).toHaveTextContent('连接失败')
  })

  it('代理近一小时没有流量时，补上最近一次请求', async () => {
    mocked(getOverviewHealth).mockResolvedValue(
      health({ proxy: { state: 'ok', last_seen_at: '2026-11-21T11:59:00Z', last_request_at: null, requests_last_hour: 0 } }),
    )
    renderPage()
    const strip = await screen.findByRole('list', { name: '组件健康' })
    expect(within(strip).getByText(/^Proxy/).closest('li')).toHaveTextContent('近 1 小时 0 个请求，从未收到请求')
  })

  it('serve 部署（全部组件一个进程）时，各进程都链到 serve 的日志；没有日志文件时不链接', async () => {
    mocked(listLogProcesses).mockResolvedValue({ processes: [{ name: 'serve' }] })
    const { unmount } = renderPage()
    const strip = await screen.findByRole('list', { name: '组件健康' })
    await waitFor(() => expect(within(strip).getByRole('link', { name: /^Runner/ })).toHaveAttribute('href', '/logs?process=serve'))
    expect(within(strip).getByRole('link', { name: /^Proxy/ })).toHaveAttribute('href', '/logs?process=serve')
    expect(within(strip).getByRole('link', { name: /^Sandbox/ })).toHaveAttribute('href', '/logs?process=serve')
    unmount()

    mocked(listLogProcesses).mockResolvedValue({ processes: [] })
    renderPage()
    const empty = await screen.findByRole('list', { name: '组件健康' })
    await waitFor(() => expect(within(empty).queryAllByRole('link')).toHaveLength(0))
  })

  it('Runner 空闲时说明空闲与并发上限', async () => {
    mocked(getOverviewHealth).mockResolvedValue(
      health({
        runner: {
          state: 'ok',
          instances: [{ host: 'h1', pid: 1, started_at: '2026-11-21T09:00:00Z', concurrency: 6, active: 0, status: 'active' }],
        },
      }),
    )
    renderPage()
    const strip = await screen.findByRole('list', { name: '组件健康' })
    expect(within(strip).getByText(/^Runner/).closest('li')).toHaveTextContent('空闲，并发上限 6')
  })

  it('待处理发现：总数与按严重级的数量，每级链到筛好的发现台账', async () => {
    renderPage()
    const panel = await screen.findByRole('region', { name: '待处理发现' })
    expect(within(panel).getByText('3')).toBeInTheDocument()
    expect(within(panel).getByRole('link', { name: '高危 2 条' })).toHaveAttribute('href', '/findings?status=open&severity=high')
    expect(within(panel).getByRole('link', { name: '中危 1 条' })).toHaveAttribute('href', '/findings?status=open&severity=medium')
    expect(within(panel).queryByRole('link', { name: /严重/ })).toBeNull() // 为零的级别不列
    expect(within(panel).getByRole('link', { name: /全部待处理/ })).toHaveAttribute('href', '/findings?status=open')
  })

  it('不再有重复的关键数字条：进行中的任务只在它的区块里', async () => {
    renderPage()
    await screen.findByRole('region', { name: '进行中的任务' })
    expect(screen.queryByRole('list', { name: '关键数字' })).toBeNull()
  })

  it('趋势图每天可聚焦，可访问名带当天明细', async () => {
    renderPage()
    const panel = await screen.findByRole('region', { name: '近 14 天新增发现' })
    expect(within(panel).getByText('3 条', { selector: 'header span' })).toBeInTheDocument()
    expect(within(panel).getByRole('img', { name: '11/21（今天）：3 条，高危 2、中危 1' })).toBeInTheDocument()
    expect(within(panel).getByRole('img', { name: '11/8：0 条' })).toBeInTheDocument()
    const usage = screen.getByRole('region', { name: '近 14 天模型消耗' })
    expect(within(usage).getByRole('img', { name: /^11\/21（今天）：10K tokens（输入 9K · 输出 1K），调用 10 次/ })).toBeInTheDocument()
    // 最近 24 小时的读数：错误率 1/10；缓存命中 4500/(9000+4500)（输入 tokens 不含命中缓存的部分）
    expect(within(usage).getByText('最近 24 小时').closest('p')).toHaveTextContent('10K tokens，调用 10 次，错误率 10.0%，缓存命中 33.3%')
  })

  it('发现类型按数量排行，每类链到按类型筛好的台账，可访问名给出全部与待处理', async () => {
    renderPage()
    const panel = await screen.findByRole('region', { name: '发现类型' })
    expect(within(panel).getByText('3 类 · 4 条')).toBeInTheDocument()
    const sqli = within(panel).getByRole('link', { name: 'SQL 注入：共 2 条，待处理 2 条' })
    expect(sqli).toHaveAttribute('href', '/findings?class=sql_injection')
    expect(within(panel).getByRole('link', { name: '其他：共 1 条，待处理 0 条' })).toHaveAttribute('href', '/findings?class=other')
  })

  it('发现类型超过八种时，余下的合成一行', async () => {
    const classes = Array.from({ length: 10 }, (_, i) => ({ class: `c${i}`, name: `类型${i}`, total: 10 - i, open: 0 }))
    mocked(getOverview).mockResolvedValue(snapshot({ findings: { ...snapshot().findings, classes } }))
    renderPage()
    const panel = await screen.findByRole('region', { name: '发现类型' })
    expect(within(panel).getAllByRole('link', { name: /^类型\d：/ })).toHaveLength(7)
    expect(within(panel).getByRole('listitem', { name: '其余 3 类：共 6 条，待处理 0 条' })).toBeInTheDocument()
  })

  it('目标表按主机链到任务与发现', async () => {
    renderPage()
    const panel = await screen.findByRole('region', { name: '目标' })
    const row = within(panel).getByRole('row', { name: /192\.168\.71\.36:8099/ })
    expect(within(row).getByRole('link', { name: '任务' })).toHaveAttribute('href', '/tasks?search=192.168.71.36%3A8099')
    expect(within(row).getByRole('link', { name: '发现' })).toHaveAttribute(
      'href',
      '/findings?search=192.168.71.36%3A8099',
    )
  })

  it('没有数据时各块给出空态', async () => {
    mocked(getOverview).mockResolvedValue(
      snapshot({
        tasks: { running: 0, paused: 0 },
        findings: { open: {}, daily: days(14).map((date) => ({ date, counts: {} })), classes: [] },
        targets: [],
      }),
    )
    mocked(listTasks).mockResolvedValue({ items: [], total: 0, page: 1, size: 6 })
    renderPage()
    expect(await screen.findByText(/^暂无进行中的任务/)).toBeInTheDocument()
    expect(screen.getByText('没有待处理的发现。')).toBeInTheDocument()
    expect(screen.getByText(/^下发带目标地址的任务后/)).toBeInTheDocument()
    expect(screen.getByText('坐实的发现会按类型汇总在这里。')).toBeInTheDocument()
  })

  it('快照读取失败可重试', async () => {
    mocked(getOverview).mockRejectedValueOnce(new Error('数据库不可用'))
    renderPage()
    expect(await screen.findByText('数据库不可用')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('region', { name: '进行中的任务' })).toBeInTheDocument()
  })
})
