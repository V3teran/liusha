import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AuditPage } from './AuditPage'
import { listAuditEvents, type AuditListResponse } from '@/api/audit'

vi.mock('@/api/audit')

const mList = listAuditEvents as unknown as ReturnType<typeof vi.fn>

function res(overrides: Partial<AuditListResponse> = {}): AuditListResponse {
  return {
    items: [
      {
        id: 7,
        actor: 'user:admin',
        action: 'task.create',
        target_kind: 'task',
        target_id: '11111111-2222-4333-8444-555555555555',
        metadata: { source: 'direct' },
        created_at: '2026-01-01T08:00:00Z',
      },
      {
        id: 6,
        actor: 'anonymous',
        action: 'auth.login_failed',
        target_kind: 'auth',
        target_id: 'root',
        metadata: null,
        created_at: '2026-01-01T07:00:00Z',
      },
    ],
    total: 2,
    page: 1,
    size: 50,
    actions: ['auth.login', 'task.create'],
    ...overrides,
  }
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <AuditPage />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('AuditPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mList.mockResolvedValue(res())
  })

  it('渲染标题、筛选栏与事件行；动作显示中文文案、metadata 就地展示', async () => {
    renderPage()
    expect(screen.getByRole('heading', { name: '操作' })).toBeTruthy()
    expect(await screen.findByText('创建任务')).toBeInTheDocument()
    expect(screen.getByText('登录失败')).toBeInTheDocument()
    expect(screen.getByText(/"source":\s*"direct"/)).toBeInTheDocument()
    // 动作下拉来自后端词表（与写入方同源）
    expect(screen.getByLabelText('按动作筛选')).toBeInTheDocument()
    expect((screen.getByLabelText('按动作筛选') as HTMLSelectElement).options.length).toBe(3)
  })

  it('无附注的事件显示 —（不渲染空对象）', async () => {
    renderPage()
    await screen.findByText('创建任务')
    const dashes = screen.getAllByText('—')
    expect(dashes.length).toBeGreaterThan(0)
  })
})
