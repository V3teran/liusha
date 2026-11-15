import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TrafficPage } from './TrafficPage'
import { getTrafficDetail, listTraffic, listTrafficContentTypes } from '@/api/traffic'
import type { TrafficDetail, TrafficSummary } from '@/api/types'

vi.mock('@/api/traffic', () => ({
  listTraffic: vi.fn(),
  getTrafficDetail: vi.fn(),
  listTrafficContentTypes: vi.fn(),
}))

const mList = listTraffic as unknown as ReturnType<typeof vi.fn>
const mDetail = getTrafficDetail as unknown as ReturnType<typeof vi.fn>
const mTypes = listTrafficContentTypes as unknown as ReturnType<typeof vi.fn>

function summary(o: Partial<TrafficSummary> = {}): TrafficSummary {
  return {
    id: 1,
    host: 'dvwa.local',
    method: 'GET',
    path: '/login.php',
    url: 'http://dvwa.local/login.php',
    content_type: 'text/html',
    status_code: 200,
    resp_len: 2048,
    captured_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

function detail(o: Partial<TrafficDetail> = {}): TrafficDetail {
  return {
    ...summary(),
    scheme: 'http',
    http_version: 'HTTP/1.1',
    request_raw: 'GET /login.php HTTP/1.1\r\nHost: dvwa.local\r\n\r\n',
    response_raw: 'HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<form ...>',
    consumed_by: [],
    ...o,
  }
}

function renderPage(entry = '/traffic') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[entry]}>
        <TrafficPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function LocationProbe() {
  return <output data-testid="location">{useLocation().search}</output>
}

describe('TrafficPage（流量模块）', () => {
  beforeEach(() => {
    mList.mockReset()
    mDetail.mockReset()
    mTypes.mockReset()
  })

  it('挂载拉取流量列表与 content-type 候选，渲染行', async () => {
    mList.mockResolvedValue({ items: [summary()], total: 1, page: 1, size: 50 })
    mTypes.mockResolvedValue(['text/html', 'application/json'])
    renderPage()

    expect(await screen.findByText('http://dvwa.local/login.php')).toBeTruthy()
    await waitFor(() => expect(mTypes).toHaveBeenCalled())
    expect(mList).toHaveBeenCalled()
  })

  it('点击行发起详情请求（按 id 钻取）', async () => {
    const user = userEvent.setup()
    mList.mockResolvedValue({ items: [summary()], total: 1, page: 1, size: 50 })
    mTypes.mockResolvedValue([])
    mDetail.mockResolvedValue(detail())
    renderPage()

    await user.click(await screen.findByText('http://dvwa.local/login.php'))

    await waitFor(() => expect(mDetail).toHaveBeenCalledWith(1))
    // 打开的流量写在 URL：跳到对话等再后退时还原详情
    expect(screen.getByTestId('location').textContent).toBe('?selected=1')
  })

  it('带 ?selected= 进页直接打开这条流量的详情', async () => {
    mList.mockResolvedValue({ items: [summary()], total: 1, page: 1, size: 50 })
    mTypes.mockResolvedValue([])
    mDetail.mockResolvedValue(detail())
    renderPage('/traffic?selected=1')
    await waitFor(() => expect(mDetail).toHaveBeenCalledWith(1))
  })

  it('列表请求失败时展示错误信息', async () => {
    mList.mockRejectedValue(new Error('GET 500: 数据库不可用'))
    mTypes.mockResolvedValue([])
    renderPage()

    expect(await screen.findByText(/GET 500: 数据库不可用/)).toBeTruthy()
  })
})
