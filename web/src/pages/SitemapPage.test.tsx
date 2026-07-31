import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SitemapPage } from './SitemapPage'
import { useOwnerResource } from '@/hooks/useOwnerResource'
import type { OwnerResource } from '@/hooks/useOwnerResource'
import type { SitemapView } from '@/api/types'

vi.mock('@/hooks/useOwnerResource', () => ({
  useOwnerResource: vi.fn(),
}))

vi.mock('@/components/OwnerPicker', () => ({
  OwnerPicker: (props: { value?: string; onChange: (id: string) => void }) => (
    <button type="button" onClick={() => props.onChange('owner-1')}>
      owner-picker:{props.value}
    </button>
  ),
}))

const mockedUseOwnerResource = useOwnerResource as unknown as ReturnType<typeof vi.fn>

function makeResource(overrides: Partial<OwnerResource<SitemapView>> = {}): OwnerResource<SitemapView> {
  return {
    owner: '',
    setOwner: vi.fn(),
    data: null,
    loading: false,
    error: '',
    notActive: false,
    reload: vi.fn(),
    ...overrides,
  }
}

describe('SitemapPage', () => {
  beforeEach(() => {
    mockedUseOwnerResource.mockReset()
  })

  it('loading 状态显示加载中', () => {
    mockedUseOwnerResource.mockReturnValue(makeResource({ loading: true }))
    render(<SitemapPage />)
    expect(screen.getByText('加载中…')).toBeTruthy()
  })

  it('error 状态显示错误信息', () => {
    mockedUseOwnerResource.mockReturnValue(makeResource({ error: '网络异常' }))
    render(<SitemapPage />)
    expect(screen.getByText(/网络异常/)).toBeTruthy()
  })

  it('notActive 状态显示提示', () => {
    mockedUseOwnerResource.mockReturnValue(makeResource({ notActive: true, owner: 'o1' }))
    render(<SitemapPage />)
    expect(screen.getByText(/仅/)).toBeTruthy()
    expect(screen.getByText('active')).toBeTruthy()
  })

  it('未选择 owner 时显示提示', () => {
    mockedUseOwnerResource.mockReturnValue(makeResource({ owner: '' }))
    render(<SitemapPage />)
    expect(screen.getByText('请选择一个 active 扫描查看攻击面')).toBeTruthy()
  })

  it('owner 已选但 data 无 domain 时显示空态', () => {
    mockedUseOwnerResource.mockReturnValue(
      makeResource({ owner: 'o1', data: { owner_id: 'o1', host: 'x', generated_at: '', root: null } }),
    )
    render(<SitemapPage />)
    expect(screen.getByText('该扫描暂无攻击面数据')).toBeTruthy()
  })

  it('渲染 domain/endpoint 树与 severity 标签 findings，统计数正确', () => {
    const data: SitemapView = {
      owner_id: 'o1',
      host: 'example.com',
      generated_at: '2026-01-01T00:00:00Z',
      root: {
        kind: 'root',
        name: 'root',
        children: [
          {
            kind: 'domain',
            name: 'a.example.com',
            children: [
              {
                kind: 'endpoint',
                name: 'ep1',
                path: '/login',
                method: 'POST',
                findings: [
                  { id: 'f1', severity: 'critical', summary: 'SQL 注入', cwe_id: 'CWE-89' },
                ],
              },
              {
                kind: 'endpoint',
                name: 'ep2',
                path: '/health',
                method: 'GET',
              },
            ],
          },
          {
            kind: 'domain',
            name: 'b.example.com',
            children: [
              {
                kind: 'endpoint',
                name: 'ep3',
                path: '/admin',
                method: 'GET',
                findings: [{ id: 'f2', severity: 'low', summary: '信息泄露' }],
              },
            ],
          },
        ],
      },
    }
    mockedUseOwnerResource.mockReturnValue(makeResource({ owner: 'o1', data }))
    render(<SitemapPage />)

    // 统计条：域名 2 / 端点 3 / 漏洞 2（"2" 出现多处，用 label 定位其容器）。
    expect(screen.getByText('域名').previousSibling?.textContent).toBe('2')
    expect(screen.getByText('端点').previousSibling?.textContent).toBe('3')
    expect(screen.getByText('漏洞').previousSibling?.textContent).toBe('2')

    expect(screen.getByText('a.example.com')).toBeTruthy()
    expect(screen.getByText('b.example.com')).toBeTruthy()
    expect(screen.getByText('/login')).toBeTruthy()
    expect(screen.getByText('/health')).toBeTruthy()
    expect(screen.getByText('/admin')).toBeTruthy()
    expect(screen.getByText('SQL 注入')).toBeTruthy()
    expect(screen.getByText('CWE-89')).toBeTruthy()
    expect(screen.getByText('信息泄露')).toBeTruthy()
    expect(screen.getByText('critical')).toBeTruthy()
    expect(screen.getByText('low')).toBeTruthy()
  })

  it('domain 无 children 时端点数按 0 计算，不崩溃', () => {
    const data: SitemapView = {
      owner_id: 'o1',
      host: 'x',
      generated_at: '',
      root: { kind: 'root', name: 'root', children: [{ kind: 'domain', name: 'lonely.example.com' }] },
    }
    mockedUseOwnerResource.mockReturnValue(makeResource({ owner: 'o1', data }))
    render(<SitemapPage />)
    expect(screen.getByText('lonely.example.com')).toBeTruthy()
    expect(screen.getByText('0 端点')).toBeTruthy()
  })
})
