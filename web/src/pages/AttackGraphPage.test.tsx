import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as apiClient from '@/api/client'
import type { AttackGraph, AttackGraphNode } from '@/api/types'
import { AttackGraphPage } from './AttackGraphPage'

// React Query 需要 Provider（useAttackGraphQuery 内部走 useQuery）。retry:false 避免 mock 拒绝时反复重试。
function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AttackGraphPage />
    </QueryClientProvider>,
  )
}

vi.mock('@/api/client', () => ({
  getAttackGraph: vi.fn(),
}))

vi.mock('@/hooks/useColorMode', () => ({ useColorMode: () => 'light' }))

vi.mock('@/components/OwnerPicker', () => ({
  OwnerPicker: ({ onChange }: { onChange: (id: string) => void }) => (
    <>
      <button type="button" onClick={() => onChange('owner-1')}>
        pick-owner
      </button>
      <button type="button" onClick={() => onChange('owner-2')}>
        pick-owner-2
      </button>
    </>
  ),
}))

const baseGraph = (over: Partial<AttackGraph> = {}): AttackGraph => ({
  task_id: 't1',
  scan_id: 's1',
  nodes: [],
  edges: [],
  verifications: [],
  ...over,
})

const mkNode = (over: Partial<AttackGraphNode>): AttackGraphNode => ({
  id: 'a',
  seq: 1,
  kind: 'asset',
  ref: { domain: 'web', ref_kind: 'endpoint', locator: '/x' },
  attrs: {},
  confidence: 'confirmed',
  ...over,
})

describe('AttackGraphPage', () => {
  beforeEach(() => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(baseGraph())
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('未选 owner 时显示占位提示', () => {
    renderPage()
    expect(screen.getByText('请选择一个扫描查看攻击图')).toBeTruthy()
  })

  it('选 owner 后拉取攻击图；无节点时显示空数据提示', async () => {
    renderPage()
    screen.getByText('pick-owner').click()
    await waitFor(() => expect(apiClient.getAttackGraph).toHaveBeenCalledWith('owner-1'))
    expect(await screen.findByText('该交战暂无坐实的攻击图节点')).toBeTruthy()
  })

  it('加载失败显示错误提示', async () => {
    vi.mocked(apiClient.getAttackGraph).mockRejectedValue(new Error('boom'))
    renderPage()
    screen.getByText('pick-owner').click()
    expect(await screen.findByText('⚠ boom')).toBeTruthy()
  })

  it('有节点数据时渲染图例（5 类节点 + 攻击链/推导边）', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [mkNode({ kind: 'finding', attrs: { summary: '某漏洞', severity: 'high' } })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    expect(await screen.findByText('目标')).toBeTruthy()
    expect(screen.getByText('资产')).toBeTruthy()
    expect(screen.getByText('凭据')).toBeTruthy()
    expect(screen.getByText('立足点')).toBeTruthy()
    expect(screen.getByText('漏洞')).toBeTruthy()
    expect(screen.getByText('攻击链')).toBeTruthy()
    expect(screen.getByText('推导')).toBeTruthy()
  })

  it('切换"实时"开关不崩溃', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(baseGraph({ nodes: [mkNode({ kind: 'asset' })] }))
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('目标')
    const label = screen.getByText('实时').closest('label')
    const liveToggle = label?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(liveToggle.checked).toBe(true)
    liveToggle.click()
    expect(liveToggle.checked).toBe(false)
  })

  it('点击节点打开详情面板，展示类型徽标 / 标识 / 严重度 / 确证程度', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({
        nodes: [
          mkNode({
            kind: 'finding',
            ref: { domain: 'web', ref_kind: 'endpoint', locator: '/login' },
            attrs: { summary: '登录处 SQL 注入', severity: 'high', cwe_id: 'CWE-89' },
            confidence: 'confirmed',
          }),
        ],
      }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('登录处 SQL 注入')
    label.click()

    expect(await screen.findByText('标识：web/endpoint')).toBeTruthy()
    expect(screen.getByText('严重度：high')).toBeTruthy()
    expect(screen.getByText('已坐实（confirmed）')).toBeTruthy()
    expect(screen.getByText('cwe_id：CWE-89')).toBeTruthy()
  })

  it('assumed 节点详情面板标注假定态', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [mkNode({ kind: 'access', confidence: 'assumed' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('/x')
    label.click()
    expect(await screen.findByText('假定（assumed，虚线）')).toBeTruthy()
  })

  it('点击详情面板关闭按钮清空选中', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [mkNode({ kind: 'asset', ref: { domain: 'web', ref_kind: 'endpoint', locator: '/api' } })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('/api')
    label.click()
    const badge = await screen.findByText('资产', { selector: 'span.rounded.px-2' })
    const closeBtn = await screen.findByLabelText('关闭')
    closeBtn.click()
    await waitFor(() => expect(badge).not.toBeInTheDocument())
  })

  it('打开详情面板后按 Esc 关闭', async () => {
    // Radix Dialog 自带 Esc 处理；这条测试锚定行为不被后续改动意外移除。
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [mkNode({ kind: 'asset', ref: { domain: 'web', ref_kind: 'endpoint', locator: '/api' } })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('/api')
    label.click()
    const badge = await screen.findByText('资产', { selector: 'span.rounded.px-2' })
    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(badge).not.toBeInTheDocument())
  })

  it('有取证记录时渲染取证链（坐实/证伪 + 耗时）', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({
        nodes: [mkNode({ kind: 'asset' })],
        verifications: [
          { id: 'v1', lead_id: 'L1', primitives: null, outcome: 'confirmed', evidence: {}, duration_ms: 120, created_at: '' },
          { id: 'v2', lead_id: 'L2', primitives: null, outcome: 'refuted', evidence: {}, duration_ms: 80, created_at: '' },
        ],
      }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    expect(await screen.findByText('✓ 坐实')).toBeTruthy()
    expect(screen.getByText('✗ 证伪')).toBeTruthy()
    expect(screen.getByText('120 ms')).toBeTruthy()
  })

  it('切换 owner 时重新拉取新 owner 的攻击图', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValueOnce(
      baseGraph({ nodes: [mkNode({ kind: 'target', ref: { domain: 'web', ref_kind: 'site', locator: 'example.com' } })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('example.com')

    vi.mocked(apiClient.getAttackGraph).mockResolvedValueOnce(baseGraph({ task_id: 't2' }))
    screen.getByText('pick-owner-2').click()
    await waitFor(() => expect(apiClient.getAttackGraph).toHaveBeenCalledWith('owner-2'))
    expect(await screen.findByText('该交战暂无坐实的攻击图节点')).toBeTruthy()
  })
})
