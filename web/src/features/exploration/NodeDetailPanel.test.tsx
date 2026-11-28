import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { rejectTaskAction } from '@/api/tasks'
import { NodeDetailPanel } from './NodeDetailPanel'
import type { GraphNode, LeadSpec } from '@/api/explorationgraph'

vi.mock('@/api/tasks', () => ({ rejectTaskAction: vi.fn(async () => undefined) }))

const node: GraphNode = {
  id: 'n1',
  task_id: 't1',
  kind: 'action',
  content: { instruction: '很长的动作指令'.repeat(50), reasoning: '规划理由' },
  state: 'running',
  source_type: 'planner',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('NodeDetailPanel', () => {
  // 详情正文超长时在浮层内滚动：滚动区要能键盘聚焦，否则键盘用户读不到被裁掉的部分。
  it('正文滚动区可键盘聚焦并有可访问名称', () => {
    render(<NodeDetailPanel node={node} onClose={vi.fn()} />)
    const body = screen.getByRole('region', { name: '节点详情' })
    expect(body.getAttribute('tabindex')).toBe('0')
  })

  // 挂在派生目标下的动作：详情里写明它所属的方向；直接挂在根目标下的动作不写这一行。
  it('动作有所属方向时写明，没有时不写', () => {
    const direction: GraphNode = {
      ...node,
      id: 'd1',
      kind: 'objective',
      state: undefined,
      content: { description: '换一个登录入口' },
    }
    const { rerender } = render(<NodeDetailPanel node={node} direction={direction} onClose={vi.fn()} />)
    expect(screen.getByText('所属方向：换一个登录入口')).toBeTruthy()

    rerender(<NodeDetailPanel node={node} onClose={vi.fn()} />)
    expect(screen.queryByText(/所属方向/)).toBeNull()
  })

  it('关闭按钮有可访问名称', () => {
    const onClose = vi.fn()
    render(<NodeDetailPanel node={node} onClose={onClose} />)
    screen.getByRole('button', { name: '关闭详情' }).click()
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('内容字段标题用中文展示名，分类值经词表翻译', () => {
    const obs: GraphNode = {
      ...node,
      kind: 'observation',
      state: undefined,
      content: {
        statement: '捕获 flag{demo}',
        category: 'hypothesis',
        reasoning: '出自 /download 响应',
        repro: { kind: 'steps' },
      },
    }
    render(<NodeDetailPanel node={obs} onClose={vi.fn()} />)
    // 契约固定字段给中文名
    expect(screen.getByText('推理依据')).toBeTruthy()
    expect(screen.getByText('复现配方')).toBeTruthy()
    expect(screen.getByText('分类')).toBeTruthy()
    // 枚举值经词表翻译，不再裸露英文
    expect(screen.getByText('假设')).toBeTruthy()
    expect(screen.queryByText('hypothesis')).toBeNull()
    expect(screen.queryByText('reasoning')).toBeNull()
  })
})

describe('NodeDetailPanel 已否决动作', () => {
  it('显示已否决状态、否决方与理由', () => {
    render(
      <NodeDetailPanel
        node={{
          ...node,
          state: 'rejected',
          state_reason: '超出授权范围',
          metadata: { rejected_by: 'user' },
        }}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('已否决')).toBeTruthy()
    // 用户的否决一律只否决这一步
    expect(screen.getByText('否决（用户，只否决这一步）：超出授权范围')).toBeTruthy()
  })

  it('监察者的否决写明范围：只否决这一步，还是连它跟进的线索一起', () => {
    const rejected = (scope: string): GraphNode => ({
      ...node,
      state: 'rejected',
      state_reason: '对象不在目标范围内',
      metadata: { rejected_by: 'monitor', rejected_scope: scope },
    })
    const { unmount } = render(<NodeDetailPanel node={rejected('lead')} onClose={vi.fn()} />)
    expect(screen.getByText('否决（监察者，连它跟进的线索一起）：对象不在目标范围内')).toBeTruthy()
    unmount()
    render(<NodeDetailPanel node={rejected('step')} onClose={vi.fn()} />)
    expect(screen.getByText('否决（监察者，只否决这一步）：对象不在目标范围内')).toBeTruthy()
  })
})

describe('NodeDetailPanel 线索', () => {
  const SPEC: LeadSpec = { next: '用 admin 会话测其余模块', question: 'low 下是否同样可利用', potential: '每多一个模块多一条主张' }
  const lead = (over: Partial<GraphNode> = {}, spec: LeadSpec = SPEC): GraphNode => ({
    ...node,
    kind: 'observation',
    state: undefined,
    content: { statement: '上传目录可写且可执行', category: 'access' },
    lead: spec,
    ...over,
  })

  it('开着的线索：详情标出「线索：待跟进」，并列出三样', () => {
    render(<NodeDetailPanel node={lead()} onClose={vi.fn()} />)
    expect(screen.getByText('线索')).toBeTruthy()
    expect(screen.getByText('待跟进')).toBeTruthy()
    expect(screen.queryByText(/线索已关闭/)).toBeNull()
    const spec = screen.getByRole('region', { name: '线索' })
    expect(within(spec).getByText('下一步').nextElementSibling?.textContent).toBe(SPEC.next)
    expect(within(spec).getByText('要弄清').nextElementSibling?.textContent).toBe(SPEC.question)
    expect(within(spec).getByText('能换来').nextElementSibling?.textContent).toBe(SPEC.potential)
  })

  it('线索成为结构化字段之前落图的没有三样：详情里不列空的三样', () => {
    render(<NodeDetailPanel node={lead({}, { next: '', question: '', potential: '' })} onClose={vi.fn()} />)
    expect(screen.getByText('待跟进')).toBeTruthy()
    expect(screen.queryByRole('region', { name: '线索' })).toBeNull()
  })

  it('已关闭的线索带关闭原因（类别与理由）', () => {
    render(
      <NodeDetailPanel
        node={lead({ lead_closed: { kind: 'blocked', reason: '需要管理员凭据' } })}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('已关闭')).toBeTruthy()
    expect(screen.getByText('线索已关闭（受阻：需要管理员凭据）')).toBeTruthy()
  })
})

describe('NodeDetailPanel 否决动作', () => {
  const renderWithQuery = (n: GraphNode) =>
    render(
      <QueryClientProvider client={new QueryClient()}>
        <NodeDetailPanel node={n} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

  it('待执行或执行中的动作可否决，原因必填', async () => {
    renderWithQuery(node)
    fireEvent.click(screen.getByRole('button', { name: '否决动作' }))
    const confirm = screen.getByRole('button', { name: '确认否决' }) as HTMLButtonElement
    expect(confirm.disabled).toBe(true)
    fireEvent.change(screen.getByLabelText('否决原因'), { target: { value: ' 超出授权范围 ' } })
    fireEvent.click(confirm)
    await waitFor(() => expect(rejectTaskAction).toHaveBeenCalledWith('t1', 'n1', '超出授权范围'))
  })

  it('已结束的动作与非动作节点没有否决入口', () => {
    renderWithQuery({ ...node, state: 'completed' })
    expect(screen.queryByRole('button', { name: '否决动作' })).toBeNull()
  })
})
