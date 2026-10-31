import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { listFindings, updateFindingTriage } from './findings'

// fetch 污染治理：每个用例后强制还原原生 fetch（对齐 client.test.ts 的口径）。
const nativeFetch = global.fetch
afterEach(() => {
  ;(global as any).fetch = nativeFetch
})

describe('全局漏洞台账 API', () => {
  beforeEach(() => {
    sessionStorage.clear()
    ;(global as any).fetch = vi.fn()
  })

  afterEach(() => {
    vi.clearAllMocks()
    ;(global as any).fetch = undefined
  })

  it('listFindings 返回分页响应（findings + total/page/size）', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ findings: [{ id: 'f1', severity: 'high' }], total: 1, page: 1, size: 50 }),
    })
    ;(global as any).fetch = mockFetch

    const res = await listFindings()

    expect(res.findings).toHaveLength(1)
    expect(res.total).toBe(1)
    expect(mockFetch).toHaveBeenCalledWith('/api/findings', { headers: { 'X-API-Key': '' } })
  })

  it('listFindings 只带真值筛选参数拼 query', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ findings: [] }) })
    ;(global as any).fetch = mockFetch

    await listFindings({ host: 'a.com', severity: '', status: 'open' })

    const url = mockFetch.mock.calls[0][0] as string
    expect(url).toContain('host=a.com')
    expect(url).toContain('status=open')
    expect(url).not.toContain('severity=')
  })

  it('listFindings 带 page/size 拼 query', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: vi.fn().mockResolvedValue({ findings: [] }) })
    ;(global as any).fetch = mockFetch

    await listFindings({ page: 2, size: 100 })

    const url = mockFetch.mock.calls[0][0] as string
    expect(url).toContain('page=2')
    expect(url).toContain('size=100')
  })

  it('updateFindingTriage PATCH 状态/严重度/备注，返回更新后的行', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ finding: { id: 'f1', status: 'confirmed' } }),
    })
    ;(global as any).fetch = mockFetch

    const row = await updateFindingTriage('f1', 'confirmed', 'high', '已核实')

    expect(row.status).toBe('confirmed')
    expect(mockFetch).toHaveBeenCalledWith(
      '/api/findings/f1/status',
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ status: 'confirmed', severity: 'high', note: '已核实' }) }),
    )
  })
})
