import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ExplorationGraphPage } from './ExplorationGraphPage'
import { getTaskGraph, type GraphNode } from '@/api/explorationgraph'
import { getTaskDetail, listTasks } from '@/api/tasks'

vi.mock('@/api/tasks', () => ({
  listTasks: vi.fn(),
  getTaskDetail: vi.fn(),
}))

vi.mock('@/api/conversations', () => ({
  getConversation: vi.fn(async (id: string) =>
    id === 'c-chat' ? { id, title: '闲聊', task_id: '' } : { id, title: '登录页越权排查', task_id: TASK_ID },
  ),
}))

vi.mock('@/api/explorationgraph', () => ({
  getTaskGraph: vi.fn(),
  getTaskVerifications: vi.fn().mockResolvedValue({ verifications: [] }),
}))

const mListTasks = listTasks as unknown as ReturnType<typeof vi.fn>
const mGraph = getTaskGraph as unknown as ReturnType<typeof vi.fn>
const mGetTask = getTaskDetail as unknown as ReturnType<typeof vi.fn>

// ReactFlow（@xyflow/react）在 jsdom 缺少 ResizeObserver / DOMMatrixReadOnly——画布
// 测量需要的最小 stub（零尺寸即可，页面只断言画布外的工具栏与空/错态）。
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (!(globalThis as any).ResizeObserver) {
  ;(globalThis as any).ResizeObserver = ResizeObserverStub
}
if (!(globalThis as any).DOMMatrixReadOnly) {
  ;(globalThis as any).DOMMatrixReadOnly = class {
    m22 = 1
    constructor(_?: string | number[]) {}
  }
}

const TASK_ID = '11111111-1111-4111-8111-111111111111'

function node(o: Partial<GraphNode> = {}): GraphNode {
  return {
    id: 'n-obj',
    task_id: TASK_ID,
    kind: 'objective',
    content: { description: '攻破 DVWA' },
    source_type: 'user',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

function taskRow(o: Record<string, unknown> = {}) {
  return { id: TASK_ID, goal: '测试商城越权', status: 'completed', source: 'direct', created_at: '2026-11-14T08:30:00', ...o }
}

function HistoryProbe() {
  const navigate = useNavigate()
  return (
    <>
      <output data-testid="location">{useLocation().search}</output>
      <button type="button" onClick={() => navigate(-1)}>
        浏览器后退
      </button>
    </>
  )
}

function renderPage(url = '/exploration-graph') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter initialEntries={[url]}>
      <QueryClientProvider client={client}>
        <ExplorationGraphPage />
        <HistoryProbe />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('ExplorationGraphPage（探索图）', () => {
  beforeEach(() => {
    mListTasks.mockReset()
    mGraph.mockReset()
    mGetTask.mockReset()
    mGetTask.mockImplementation(async (id: string) => taskRow({ id }))
  })

  it('挂载取最新任务并按其加载探索图（task 回写 URL）', async () => {
    mListTasks.mockResolvedValue({ items: [taskRow()], total: 1, page: 1, size: 1 })
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    renderPage()

    await waitFor(() => expect(mListTasks).toHaveBeenCalledWith(1, 1))
    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
    expect(await screen.findByText('暂无探索图')).toBeTruthy()
    expect(screen.getByText('任务开始规划后，探索图会出现在这里。')).toBeTruthy()
    // 选择框显示范围：选中任务的目标，而不是 ID
    expect(await screen.findByRole('button', { name: '选择任务：任务：测试商城越权' })).toBeTruthy()
  })

  it('链接带 task 时直接按该任务加载，不再取最新任务', async () => {
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    renderPage(`/exploration-graph?task=${TASK_ID}`)

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
    expect(mListTasks).not.toHaveBeenCalled()
  })

  // 从对话跳来只看「这个对话的探索图」：范围显示对话标题，图按对话所属的任务取。
  it('链接带 conversation 时按对话限定：显示对话标题，按对话所属任务取图', async () => {
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    renderPage('/exploration-graph?conversation=c-7')

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
    expect(await screen.findByRole('button', { name: '选择任务：对话：登录页越权排查' })).toBeTruthy()
    expect(mListTasks).not.toHaveBeenCalled()
  })

  // 换选任务是一步导航：后退回到原来的任务
  it('换选任务后浏览器后退回到原来的任务', async () => {
    mListTasks.mockResolvedValue({ items: [taskRow({ id: 't-other', goal: '另一个任务' })], total: 1, page: 1, size: 20 })
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    const user = userEvent.setup()
    renderPage(`/exploration-graph?task=${TASK_ID}`)
    await user.click(await screen.findByRole('button', { name: /^选择任务：/ }))
    await user.click(await screen.findByRole('option', { name: /另一个任务/ }))
    expect(screen.getByTestId('location').textContent).toBe('?task=t-other')
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('location').textContent).toBe(`?task=${TASK_ID}`)
  })

  // 从漏洞跳来：链接带 selected，直接打开坐实它的结果节点；关闭是一步导航，后退重新打开
  it('链接带 selected 时打开该节点的详情，关闭后后退重新打开', async () => {
    mGraph.mockResolvedValue({
      nodes: [node(), node({ id: 'n-res', kind: 'result', source_type: 'evaluator', content: { statement: '反射型 XSS 已坐实' } })],
      edges: [{ src_id: 'n-obj', dst_id: 'n-res', rel: 'generates', created_at: '2026-06-10T00:00:00Z' }],
    })
    const user = userEvent.setup()
    renderPage(`/exploration-graph?task=${TASK_ID}&selected=n-res`)
    const detail = await screen.findByRole('region', { name: '节点详情' })
    expect(within(detail).getByText('反射型 XSS 已坐实')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: '关闭详情' }))
    expect(screen.queryByRole('region', { name: '节点详情' })).toBeNull()
    expect(screen.getByTestId('location').textContent).toBe(`?task=${TASK_ID}`)
    await user.click(screen.getByText('浏览器后退'))
    expect(await screen.findByRole('region', { name: '节点详情' })).toBeTruthy()
  })

  it('换选任务清掉原来打开的节点', async () => {
    mListTasks.mockResolvedValue({ items: [taskRow({ id: 't-other', goal: '另一个任务' })], total: 1, page: 1, size: 20 })
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    const user = userEvent.setup()
    renderPage(`/exploration-graph?task=${TASK_ID}&selected=n-res`)
    await user.click(await screen.findByRole('button', { name: /^选择任务：/ }))
    await user.click(await screen.findByRole('option', { name: /另一个任务/ }))
    expect(screen.getByTestId('location').textContent).toBe('?task=t-other')
  })

  it('纯聊天对话没有任务，提示没有探索图', async () => {
    renderPage('/exploration-graph?conversation=c-chat')
    expect(await screen.findByText('这个对话是纯聊天，没有任务。')).toBeTruthy()
    expect(mGraph).not.toHaveBeenCalled()
  })

  // 一个任务都没有是「暂无任务」，不是让用户去选一个不存在的任务。
  it('没有任何任务：显示「暂无任务」', async () => {
    mListTasks.mockResolvedValue({ items: [], total: 0, page: 1, size: 1 })
    renderPage()
    expect(await screen.findByText('暂无任务')).toBeTruthy()
    expect(screen.queryByText('未选择任务')).toBeNull()
    expect(mGraph).not.toHaveBeenCalled()
  })

  it('任务选择框旁带这个任务的任务、对话、漏洞跳转（属于选中的任务，不在页头）', async () => {
    mGetTask.mockImplementation(async (id: string) => taskRow({ id, conversation_id: 'c-7', finding_count: 3 }))
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    renderPage(`/exploration-graph?task=${TASK_ID}`)

    const toolbar = await screen.findByRole('search', { name: '探索图筛选' })
    expect((await within(toolbar).findByRole('link', { name: '任务' })).getAttribute('href')).toBe(`/tasks?task=${TASK_ID}&selected=${TASK_ID}`)
    expect(screen.getByRole('link', { name: '对话' }).getAttribute('href')).toBe('/conversations/direct?conversation=c-7')
    expect(screen.getByRole('link', { name: '漏洞' }).getAttribute('href')).toBe(`/findings?task=${TASK_ID}`)
  })

  it('图数据加载失败时展示错误与排查提示', async () => {
    mListTasks.mockResolvedValue({ items: [taskRow()], total: 1, page: 1, size: 1 })
    mGraph.mockRejectedValue(new Error('资源不存在'))
    renderPage()

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
    expect(await screen.findByText('资源不存在')).toBeTruthy()
    expect(screen.getByText(/不存在或已删除，请重新选择任务。/)).toBeTruthy()
  })

  it('带节点数据时渲染 kind 计数 chips 与总量，点击 chip 即筛选', async () => {
    mListTasks.mockResolvedValue({ items: [taskRow()], total: 1, page: 1, size: 1 })
    mGraph.mockResolvedValue({
      nodes: [
        node(),
        node({ id: 'n-act', kind: 'action', state: 'completed', source_type: 'planner', content: { instruction: '扫描登录页' } }),
      ],
      edges: [{ src_id: 'n-obj', dst_id: 'n-act', rel: 'generates', created_at: '2026-06-10T00:00:00Z' }],
    })
    const user = userEvent.setup()
    renderPage()

    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
    // 工具栏：kind 计数 chips（统计+图例+筛选一体）与总量
    expect(await screen.findByText('1 目标')).toBeTruthy()
    expect(screen.getByText('1 动作')).toBeTruthy()
    expect(screen.getByText('2 节点 · 1 边')).toBeTruthy()

    // 点击 chip 切换筛选（aria-pressed 标记激活态）
    const actionChip = screen.getByRole('button', { name: '1 动作' })
    await user.click(actionChip)
    expect(actionChip.getAttribute('aria-pressed')).toBe('true')
    // 再点一次取消
    await user.click(actionChip)
    expect(actionChip.getAttribute('aria-pressed')).toBe('false')
  })

  it('任务从可搜索的选择框选取，选中后按它加载探索图', async () => {
    mListTasks.mockImplementation(async (_page: number, _size: number, filters?: { search?: string }) => ({
      items: filters?.search ? [taskRow()] : [],
      total: filters?.search ? 1 : 0,
      page: 1,
      size: 20,
    }))
    mGraph.mockResolvedValue({ nodes: [], edges: [] })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: '选择任务：选择任务' }))
    await user.type(screen.getByRole('combobox', { name: '搜索任务目标、批次名' }), '商城')
    // 选中前不拉图
    expect(mGraph).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('option', { name: /测试商城越权/ }))
    await waitFor(() => expect(mGraph).toHaveBeenCalledWith(TASK_ID))
  })
})
