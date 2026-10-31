import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ExplorationGraphPage } from './ExplorationGraphPage'
import { getConversationGraph, type GraphNode } from '@/api/explorationgraph'
import { listConversations } from '@/api/conversations'

vi.mock('@/api/conversations', () => ({
  listConversations: vi.fn(),
}))

vi.mock('@/api/explorationgraph', () => ({
  getConversationGraph: vi.fn(),
  getConversationVerifications: vi.fn().mockResolvedValue({ verifications: [] }),
}))

const mListConvs = listConversations as unknown as ReturnType<typeof vi.fn>
const mGraph = getConversationGraph as unknown as ReturnType<typeof vi.fn>

// ReactFlow（@xyflow/react）在 jsdom 缺少 ResizeObserver / DOMMatrixReadOnly——画布
// 测量需要的最小 stub（零尺寸即可，页面只断言 ReactFlow 之外的头部统计与空/错态）。
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (!(global as any).ResizeObserver) {
  ;(global as any).ResizeObserver = ResizeObserverStub
}
if (!(global as any).DOMMatrixReadOnly) {
  ;(global as any).DOMMatrixReadOnly = class {
    m22 = 1
    constructor(_?: string | number[]) {}
  }
}

const CONV_ID = '11111111-1111-4111-8111-111111111111'

function node(o: Partial<GraphNode> = {}): GraphNode {
  return {
    id: 'n-obj',
    task_id: 't-1',
    kind: 'objective',
    content: { description: '攻破 DVWA' },
    source_type: 'user',
    source_id: 'u',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/exploration-graph']}>
      <ExplorationGraphPage />
    </MemoryRouter>,
  )
}

describe('ExplorationGraphPage（探索图）', () => {
  beforeEach(() => {
    mListConvs.mockReset()
    mGraph.mockReset()
  })

  it('挂载取最新会话并按其加载探索图（conversation_id 回写 URL）', async () => {
    mListConvs.mockResolvedValue({ conversations: [{ id: CONV_ID }], hasMore: false, total: 1 })
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    renderPage()

    // 先取最新会话 → 再按该会话拉图
    await waitFor(() => expect(mListConvs).toHaveBeenCalledWith(1, 0, ''))
    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(CONV_ID))
    // 空图 → 空态（画布区不渲染 ReactFlow）
    expect(await screen.findByText('暂无数据')).toBeTruthy()
  })

  it('图数据加载失败时展示错误与排查提示', async () => {
    mListConvs.mockResolvedValue({ conversations: [{ id: CONV_ID }], hasMore: false, total: 1 })
    mGraph.mockRejectedValue(new Error('GET 404: 会话不存在'))
    renderPage()

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(CONV_ID))
    expect(await screen.findByText('GET 404: 会话不存在')).toBeTruthy()
    expect(screen.getByText(/请检查任务 ID 是否正确/)).toBeTruthy()
  })

  it('带节点数据时渲染统计徽章（总数/连线/分类计数）', async () => {
    mListConvs.mockResolvedValue({ conversations: [{ id: CONV_ID }], hasMore: false, total: 1 })
    mGraph.mockResolvedValue({
      nodes: [
        node(),
        node({ id: 'n-act', kind: 'action', content: { instruction: '扫描登录页' }, state: 'completed', source_type: 'planner' }),
      ],
      edges: [{ src_id: 'n-obj', dst_id: 'n-act', rel: 'GENERATES' as const, created_at: '2026-06-10T00:00:00Z' }],
    })
    const user = userEvent.setup()
    renderPage()

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(CONV_ID))
    // 头部统计徽章（ReactFlow 之外，jsdom 可安全断言）
    expect(await screen.findByText('2 节点')).toBeTruthy()
    expect(screen.getByText('1 边')).toBeTruthy()
    expect(screen.getByText('1 目标')).toBeTruthy()
    expect(screen.getByText('1 动作')).toBeTruthy()

    // 类型筛选（下拉）存在且可选——探索图视图的核心交互
    const kindSelect = screen.getByDisplayValue('全部类型') as HTMLSelectElement
    await user.selectOptions(kindSelect, 'action')
    // 筛选后仅剩动作节点：总数徽章不变（stats 统计原始全量），画布过滤由 ReactFlow 内部完成
    expect(screen.getByText('2 节点')).toBeTruthy()
  })
})
