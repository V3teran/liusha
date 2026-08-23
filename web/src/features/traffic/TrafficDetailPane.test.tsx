import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TrafficDetailPane } from './TrafficDetailPane'
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
    resp_len: 1234,
    captured_at: '2026-01-01T00:00:00Z',
    content_type: 'application/json',
    http_version: 'HTTP/1.1',
    request_raw: 'POST /v1/login HTTP/1.1\r\nHost: api.example.com\r\nContent-Type: application/json\r\n\r\n{"user":"alice"}',
    response_raw: 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"ok":true}',
    consumed_by: [],
    ...overrides,
  }
}

function renderPane(props: Partial<Parameters<typeof TrafficDetailPane>[0]> = {}) {
  return render(
    <MemoryRouter>
      <TrafficDetailPane detail={makeDetail()} loading={false} error="" onClose={() => {}} {...props} />
    </MemoryRouter>,
  )
}

describe('TrafficDetailPane', () => {
  it('loading 时显示加载中', () => {
    renderPane({ detail: null, loading: true })
    expect(screen.getByText('加载中…')).toBeTruthy()
  })

  it('error 时显示错误', () => {
    renderPane({ detail: null, error: '加载详情失败' })
    expect(screen.getByText(/加载详情失败/)).toBeTruthy()
  })

  it('顶部 URL 那一行已删除（URL 在列表列与报文首行中已呈现），仅保留关闭按钮', () => {
    renderPane()
    // 详情不再单列一行完整 URL 胶囊 + 复制按钮：
    expect(screen.queryByLabelText('复制 URL')).toBeNull()
    // method/status/长度/协议/时间等独立元信息也不单列（仍在下方报文文本里）：
    expect(screen.queryByText('1.2s')).toBeNull()
    expect(screen.queryByText('HTTP/1.1', { selector: 'span' })).toBeNull()
    // 关闭按钮保留（独占右对齐细行）：
    expect(screen.getByLabelText('关闭详情')).toBeTruthy()
  })

  it('渲染整条请求/响应报文', () => {
    renderPane()
    expect(screen.getByText(/"user":"alice"/)).toBeTruthy()
    expect(screen.getByText(/"ok":true/)).toBeTruthy()
  })

  it('请求块带 JSON Content-Type 时提供美化切换（不再依赖外部字段）', () => {
    renderPane()
    // 请求与响应报文各自都带 application/json，两块都应出现「美化」tab。
    expect(screen.getAllByText('美化').length).toBe(2)
  })

  it('无消费任务时不渲染消费任务区', () => {
    renderPane()
    expect(screen.queryByText('消费任务')).toBeNull()
  })

  it('有消费任务时渲染 chip，有 conv_id 则可跳会话', () => {
    renderPane({
      detail: makeDetail({
        consumed_by: [{ task_id: 't1', scenario_id: 'api-pentest', host: 'api.example.com', status: 'running', conv_id: 'c9' }],
      }),
    })
    expect(screen.getByText('消费任务')).toBeTruthy()
    expect(screen.getByText('api-pentest')).toBeTruthy()
    const link = screen.getByRole('link')
    expect(link.getAttribute('href')).toContain('/conversations/auto?conv=c9')
  })

  it('响应无 body 时该块不提供美化切换（请求块仍可美化）', () => {
    renderPane({ detail: makeDetail({ response_raw: 'HTTP/1.1 204 No Content\r\n\r\n' }) })
    // 请求块带 JSON body 仍出现「美化」，响应块无 body 不出现——故恰好 1 个。
    expect(screen.queryAllByText('美化').length).toBe(1)
  })

  it('点击关闭按钮触发 onClose', () => {
    let closed = false
    render(
      <MemoryRouter>
        <TrafficDetailPane detail={makeDetail()} loading={false} error="" onClose={() => (closed = true)} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByLabelText('关闭详情'))
    expect(closed).toBe(true)
  })
})
