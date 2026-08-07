import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TrafficDrawer } from './TrafficDrawer'
import type { TrafficDetail } from '@/api/types'

function makeDetail(overrides: Partial<TrafficDetail> = {}): TrafficDetail {
  return {
    id: 1,
    host: 'api.example.com',
    method: 'POST',
    scheme: 'https',
    url: 'https://api.example.com/v1/login',
    path: '/v1/login',
    status_code: 200,
    duration_ms: 1234,
    consumed_by_task_id: '',
    captured_at: '2026-01-01T00:00:00Z',
    request_headers: { 'Content-Type': ['application/json'], Accept: 'text/html' },
    request_body: '{"user":"alice"}',
    response_headers: {},
    response_body: '',
    ...overrides,
  }
}

const noop = () => {}

describe('TrafficDrawer', () => {
  it('open=false 时不渲染内容', () => {
    render(<TrafficDrawer open={false} detail={makeDetail()} loading={false} error="" onOpenChange={noop} />)
    expect(screen.queryByText('元信息')).toBeNull()
  })

  it('loading 时显示加载中而非详情', () => {
    render(<TrafficDrawer open detail={null} loading error="" onOpenChange={noop} />)
    expect(screen.getByText('加载中…')).toBeTruthy()
    expect(screen.queryByText('元信息')).toBeNull()
  })

  it('error 时显示错误信息', () => {
    render(<TrafficDrawer open detail={null} loading={false} error="加载详情失败" onOpenChange={noop} />)
    expect(screen.getByText(/加载详情失败/)).toBeTruthy()
  })

  it('渲染元信息与 method/url', () => {
    render(<TrafficDrawer open detail={makeDetail()} loading={false} error="" onOpenChange={noop} />)
    expect(screen.getByText('元信息')).toBeTruthy()
    expect(screen.getByText('api.example.com')).toBeTruthy()
    expect(screen.getByText('/v1/login')).toBeTruthy()
    // 未消费时占位
    expect(screen.getByText('未消费')).toBeTruthy()
  })

  it('headersToText：数组值 join、标量原样，拍平成多行 key: value', () => {
    render(<TrafficDrawer open detail={makeDetail()} loading={false} error="" onOpenChange={noop} />)
    // request_headers = { 'Content-Type': ['application/json'], Accept: 'text/html' }
    expect(screen.getByText(/Content-Type: application\/json/)).toBeTruthy()
    expect(screen.getByText(/Accept: text\/html/)).toBeTruthy()
  })

  it('空 headers / body 显示占位文案', () => {
    render(<TrafficDrawer open detail={makeDetail()} loading={false} error="" onOpenChange={noop} />)
    // response_headers={} → 无响应头；response_body='' → 无响应体
    expect(screen.getByText('无响应头')).toBeTruthy()
    expect(screen.getByText('无响应体')).toBeTruthy()
  })

  it('请求体原文渲染', () => {
    render(<TrafficDrawer open detail={makeDetail()} loading={false} error="" onOpenChange={noop} />)
    expect(screen.getByText('{"user":"alice"}')).toBeTruthy()
  })

  it('已消费任务显示 task id', () => {
    render(
      <TrafficDrawer
        open
        detail={makeDetail({ consumed_by_task_id: 'task-42' })}
        loading={false}
        error=""
        onOpenChange={noop}
      />,
    )
    expect(screen.getByText('task-42')).toBeTruthy()
    expect(screen.queryByText('未消费')).toBeNull()
  })
})
