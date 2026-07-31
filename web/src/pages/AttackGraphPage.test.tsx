import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as apiClient from '@/api/client'
import { AttackGraphPage } from './AttackGraphPage'

// React Query 需要 Provider（useAttackGraphQuery 内部走 useQuery/useQueryClient）。
// retry: false 避免测试里 mock 拒绝时反复重试拖慢/污染后续断言。
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
  getMilestones: vi.fn(),
  getMessage: vi.fn(),
  listTasks: vi.fn(),
}))

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

const baseGraph = (over: Partial<apiClient.AttackGraph> = {}): apiClient.AttackGraph => ({
  task_id: 't1',
  conversation_id: 'c1',
  running: false,
  enriched: false,
  nodes: [],
  edges: [],
  ...over,
})

// on_path=true 的单节点：成果优先（默认）模式下始终可见、不会被折叠进占位段。
const onPathNode = (over: Partial<apiClient.AttackGraphNode>): apiClient.AttackGraphNode => ({
  id: 'a',
  kind: 'probe',
  title: 'x',
  on_path: true,
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
    expect(screen.getByText('请选择一个扫描查看执行图')).toBeTruthy()
  })

  it('选 owner 后拉取执行图；无节点时显示空数据提示', async () => {
    renderPage()
    screen.getByText('pick-owner').click()
    await waitFor(() => expect(apiClient.getAttackGraph).toHaveBeenCalledWith('owner-1'))
    expect(await screen.findByText('该扫描暂无执行图数据')).toBeTruthy()
  })

  it('加载失败显示错误提示', async () => {
    vi.mocked(apiClient.getAttackGraph).mockRejectedValue(new Error('boom'))
    renderPage()
    screen.getByText('pick-owner').click()
    expect(await screen.findByText('⚠ boom')).toBeTruthy()
  })

  it('有节点数据时渲染图例与里程碑摘要按钮', async () => {
    // 节点标题刻意避开图例词（任务/判断/探测/信号/漏洞），否则图例断言会命中节点标题产生歧义。
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'hypothesis', title: '某假设节点' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    expect(await screen.findByText('里程碑摘要')).toBeTruthy()
    // 图例含 5 类节点 + 3 条重点边。
    expect(screen.getByText('任务')).toBeTruthy()
    expect(screen.getByText('判断')).toBeTruthy()
    expect(screen.getByText('探测')).toBeTruthy()
    expect(screen.getByText('信号')).toBeTruthy()
    expect(screen.getByText('漏洞')).toBeTruthy()
    expect(screen.getByText('攻击链')).toBeTruthy()
  })

  it('点击里程碑摘要按钮拉取并展示摘要卡', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'hypothesis', title: '判断' })] }),
    )
    vi.mocked(apiClient.getMilestones).mockResolvedValue([
      { agent: 'exploitation', summary: '利用了 SQLi', node_count: 12 },
    ])
    renderPage()
    screen.getByText('pick-owner').click()
    const btn = await screen.findByText('里程碑摘要')
    btn.click()
    expect(await screen.findByText('利用了 SQLi')).toBeTruthy()
    expect(screen.getByText('exploitation')).toBeTruthy()
    expect(screen.getByText('12 步')).toBeTruthy()
  })

  it('里程碑加载失败显示错误信息', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'hypothesis', title: '判断' })] }),
    )
    vi.mocked(apiClient.getMilestones).mockRejectedValue(new Error('生成超时'))
    renderPage()
    screen.getByText('pick-owner').click()
    const btn = await screen.findByText('里程碑摘要')
    btn.click()
    expect(await screen.findByText('⚠ 生成超时')).toBeTruthy()
  })

  it('切换"成果优先"开关不崩溃', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'finding', title: '漏洞', severity: 'high' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('里程碑摘要')
    const label = screen.getByText('成果优先').closest('label')
    const simplifiedToggle = label?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(simplifiedToggle.checked).toBe(true)
    simplifiedToggle.click()
    expect(simplifiedToggle.checked).toBe(false)
  })

  it('切换"实时"开关不崩溃', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'probe', title: '做' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('里程碑摘要')
    const label = screen.getByText('实时').closest('label')
    const liveToggle = label?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(liveToggle.checked).toBe(true)
    liveToggle.click()
    expect(liveToggle.checked).toBe(false)
  })

  it('点击真实节点打开详情面板，展示 agent/severity/死路信息', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({
        nodes: [
          onPathNode({ kind: 'probe', title: '失败的探测', agent: 'exploitation', status: 'failed', severity: 'high' }),
        ],
      }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('失败的探测')
    label.click()

    expect(await screen.findByText('子代理：exploitation')).toBeTruthy()
    expect(screen.getByText('严重度：high')).toBeTruthy()
    expect(screen.getByText('死路 / 探测失败')).toBeTruthy()
  })

  it('点击带 ref 的节点按需拉取单条原文，不预拉整段会话', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({
        conversation_id: 'conv-42',
        nodes: [onPathNode({ kind: 'finding', title: '做了什么', ref: 'msg-99' })],
      }),
    )
    vi.mocked(apiClient.getMessage).mockResolvedValue({
      Seq: 1,
      ID: 'msg-99',
      ConversationID: 'conv-42',
      Role: 'tool',
      Kind: 'event',
      Content: '工具输出原文内容',
      Metadata: null,
      CreatedAt: '',
    })
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('做了什么')
    label.click()

    // 只在点击后才按 conv + ref 拉这一条，不是预拉整段会话。
    await waitFor(() => expect(apiClient.getMessage).toHaveBeenCalledWith('conv-42', 'msg-99'))
    expect(apiClient.getMessage).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('工具输出原文内容')).toBeTruthy()
  })

  it('点击无 ref 的节点不触发原文拉取', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'task', title: '任务内容' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('任务内容')
    label.click()
    await screen.findByText('任务内容')
    expect(apiClient.getMessage).not.toHaveBeenCalled()
  })

  it('点击详情面板关闭按钮清空选中', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'task', title: '任务内容' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('任务内容')
    label.click()
    const badge = await screen.findByText('任务', { selector: 'span.rounded.px-2' })
    const closeBtn = await screen.findByLabelText('关闭')
    closeBtn.click()
    await waitFor(() => expect(badge).not.toBeInTheDocument())
  })

  it('打开详情面板后按 Esc 关闭', async () => {
    // Radix Dialog 自带 Esc 处理；这条测试锚定这个行为不被后续改动意外移除。
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({ nodes: [onPathNode({ kind: 'task', title: '任务内容' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const label = await screen.findByText('任务内容')
    label.click()
    const badge = await screen.findByText('任务', { selector: 'span.rounded.px-2' })
    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(badge).not.toBeInTheDocument())
  })

  it('点击占位折叠段节点 toggle 展开/收起（不弹详情面板）', async () => {
    // 非 on_path 节点在成果优先模式下被折叠进占位段（由后端 collapsed 元数据驱动）。
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(
      baseGraph({
        nodes: [
          onPathNode({ id: 'root', kind: 'task', title: '主线' }),
          { id: 'e1', kind: 'probe', title: '开场探测', on_path: false },
          onPathNode({ id: 'f', kind: 'finding', title: '漏洞', parent_id: 'root' }),
        ],
        collapsed: [{ anchor: '', hidden_count: 1, opening: true }],
      }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    const placeholder = await screen.findByText(/侦察与初始访问/)
    expect(placeholder.textContent).toContain('1 步')
    placeholder.click()
    // 展开后占位标签变「收起」，未打开详情面板（详情面板容器 aside 不存在）。
    expect(await screen.findByText(/收起 1 步/)).toBeTruthy()
    expect(document.querySelector('aside')).toBeNull()
  })

  it('完整模式下节点数超阈值时先弹确认，不直接渲染全量图', async () => {
    const manyNodes = Array.from({ length: 501 }, (_, i) => onPathNode({ id: `n${i}`, kind: 'probe', title: `步骤${i}` }))
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(baseGraph({ nodes: manyNodes }))
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('里程碑摘要')

    const label = screen.getByText('成果优先').closest('label')
    const simplifiedToggle = label?.querySelector('input[type="checkbox"]') as HTMLInputElement
    simplifiedToggle.click() // 关闭成果优先 → 完整模式

    expect(await screen.findByText(/完整模式将渲染全部 501 个节点/)).toBeTruthy()
    // 确认前不应该真的渲染出节点标签（React Flow 未挂载全量节点）。
    expect(screen.queryByText('步骤0')).toBeNull()

    screen.getByText('仍然渲染全部节点').click()
    await waitFor(() => expect(screen.queryByText(/完整模式将渲染全部/)).toBeNull())
  })

  it('完整模式确认提示里点"返回成果优先"会切回成果优先且不留确认状态', async () => {
    const manyNodes = Array.from({ length: 501 }, (_, i) => onPathNode({ id: `n${i}`, kind: 'probe', title: `步骤${i}` }))
    vi.mocked(apiClient.getAttackGraph).mockResolvedValue(baseGraph({ nodes: manyNodes }))
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('里程碑摘要')

    const label = screen.getByText('成果优先').closest('label')
    const simplifiedToggle = label?.querySelector('input[type="checkbox"]') as HTMLInputElement
    simplifiedToggle.click()
    await screen.findByText(/完整模式将渲染全部 501 个节点/)

    screen.getByText('返回成果优先').click()
    await waitFor(() => expect(simplifiedToggle.checked).toBe(true))
    expect(screen.queryByText(/完整模式将渲染全部/)).toBeNull()
  })

  it('切换 owner 时重新拉取新 owner 的执行图', async () => {
    vi.mocked(apiClient.getAttackGraph).mockResolvedValueOnce(
      baseGraph({ nodes: [onPathNode({ kind: 'task', title: '任务节点' })] }),
    )
    renderPage()
    screen.getByText('pick-owner').click()
    await screen.findByText('任务节点')

    vi.mocked(apiClient.getAttackGraph).mockResolvedValueOnce(baseGraph({ task_id: 't2' }))
    screen.getByText('pick-owner-2').click()
    await waitFor(() => expect(apiClient.getAttackGraph).toHaveBeenCalledWith('owner-2'))
    expect(await screen.findByText('该扫描暂无执行图数据')).toBeTruthy()
  })
})
