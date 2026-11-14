import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { rejectTaskAction } from '@/api/tasks'
import { NodeDetailPanel } from './NodeDetailPanel'
import type { GraphNode } from '@/api/explorationgraph'

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

  it('关闭按钮有可访问名称', () => {
    const onClose = vi.fn()
    render(<NodeDetailPanel node={node} onClose={onClose} />)
    screen.getByRole('button', { name: '关闭详情' }).click()
    expect(onClose).toHaveBeenCalledOnce()
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
    expect(screen.getByText('否决（用户）：超出授权范围')).toBeTruthy()
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
