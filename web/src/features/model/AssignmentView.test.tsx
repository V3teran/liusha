import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssignmentView } from './AssignmentView'
import { listProviders, getRouting, saveRoleRoute, deleteRoleRoute } from '@/api/models'
import { listAgentConfigs, saveAgentComplexity } from '@/api/config'
import type { ProviderConfig, RoutingResponse, AgentConfig } from '@/api/types'

vi.mock('@/api/models', () => ({
  listProviders: vi.fn(),
  getRouting: vi.fn(),
  saveRoleRoute: vi.fn(),
  deleteRoleRoute: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  listAgentConfigs: vi.fn(),
  saveAgentComplexity: vi.fn(),
}))

const mListProv = listProviders as unknown as ReturnType<typeof vi.fn>
const mGetRouting = getRouting as unknown as ReturnType<typeof vi.fn>
const mSaveRole = saveRoleRoute as unknown as ReturnType<typeof vi.fn>
const mDeleteRole = deleteRoleRoute as unknown as ReturnType<typeof vi.fn>
const mListAgents = listAgentConfigs as unknown as ReturnType<typeof vi.fn>
const mSaveTier = saveAgentComplexity as unknown as ReturnType<typeof vi.fn>

function prov(key: string, enabled = true): ProviderConfig {
  return {
    code: key,
    type: 'openai_compat',
    base_url: 'https://x',
    default_model: 'm',
    key_present: true,
    max_tokens: 4096,
    supports_tools: true,
    supports_vision: false,
    context_window: 65536,
    description: '',
    sort_order: 0,
    enabled,
  }
}

function agent(id: string, name: string, complexity: string): AgentConfig {
  return {
    id,
    code: id,
    name,
    description: '',
    system_prompt: '',
    function_tools: [],
    cli_tools: [],
    skills: [],
    max_iterations: 10,
    enabled: true,
    complexity,
  } as AgentConfig
}

// 路由按复杂度档（complexity）键存：complex/medium/simple/__fallback__。
const routing = (o: Partial<RoutingResponse> = {}): RoutingResponse => ({
  routes: [{ role: 'medium', provider_code: 'deepseek' }],
  ...o,
})

const agents = () => [
  agent('h-orch', 'planner', 'complex'),
  agent('h-traffic', 'traffic-analysis', 'medium'),
]

function renderView() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AssignmentView />
    </QueryClientProvider>,
  )
}

describe('AssignmentView', () => {
  beforeEach(() => {
    mListProv.mockReset()
    mGetRouting.mockReset()
    mSaveRole.mockReset()
    mDeleteRole.mockReset()
    mListAgents.mockReset()
    mSaveTier.mockReset()
    mListProv.mockResolvedValue([prov('deepseek'), prov('qwen')])
    mListAgents.mockResolvedValue(agents())
  })

  it('挂载并行加载 provider + 路由 + 智能体，单卡渲染三档（无兜底槽）', async () => {
    mGetRouting.mockResolvedValue(routing())
    renderView()
    // 卡片头已去重（分区标签即「能力路由」分区头）；兜底槽已删——仅三档。
    expect(screen.queryByText('能力档')).toBeNull()
    expect(screen.queryByText('兜底槽')).toBeNull()
    expect(screen.queryByText('兜底部署')).toBeNull()
    expect(screen.queryByText('__fallback__')).toBeNull()
    // 三档中文标签都出现。
    expect(await screen.findByText('深度推理')).toBeTruthy()
    expect(await screen.findByText('标准推理')).toBeTruthy()
    expect(await screen.findByText('轻量快答')).toBeTruthy()
    // 内建路由键（inspector/compactor）不在 UI 展示（冗余已删）。
    expect(screen.queryByText('inspector')).toBeNull()
    expect(screen.queryByText('compactor')).toBeNull()
  })

  it('归属胶囊表达档位归属：planner 在 complex 档实心禁用，medium 档为可点幽灵胶囊', async () => {
    mGetRouting.mockResolvedValue(routing())
    renderView()
    // 等数据渲染出 agent 胶囊（退出加载态），再做行作用域断言。
    await screen.findAllByRole('button', { name: 'planner' })
    // planner 归属 complex 档 → complex 行内胶囊实心禁用（单值归属无操作）；
    // medium 行内是可点幽灵胶囊（aria-pressed=false）。
    const complexGroup = within(screen.getByTestId('complexity-row-complex'))
    const mediumGroup = within(screen.getByTestId('complexity-row-medium'))
    const inComplex = complexGroup.getByRole('button', { name: 'planner' })
    expect(inComplex).toBeTruthy()
    expect(inComplex).toBeDisabled()
    const ghost = mediumGroup.getByRole('button', { name: 'planner' })
    expect(ghost).toBeEnabled()
  })

  it('点未归属胶囊把智能体移入目标档，调 saveAgentComplexity(id, complexity)', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveTier.mockResolvedValue(agent('h-traffic', 'traffic-analysis', 'simple'))
    renderView()
    // traffic-analysis 原在 medium，点 simple 行的幽灵胶囊 → 移入 simple。
    await waitFor(() =>
      expect(screen.getByTestId('complexity-row-simple').textContent).toContain('traffic-analysis'),
    ) // 等数据渲染出行内容
    const simpleRow = screen.getByTestId('complexity-row-simple')
    const simpleGroup = within(simpleRow)
    const chip = simpleGroup.getByRole('button', { name: 'traffic-analysis' })
    await userEvent.click(chip)
    await waitFor(() => expect(mSaveTier).toHaveBeenCalledWith('h-traffic', 'simple'))
  })

  it('移档失败乐观回滚并在页面内回显原因（无 window.alert）', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    mGetRouting.mockResolvedValue(routing())
    mSaveTier.mockRejectedValue(new Error('移档炸了'))
    renderView()
    await screen.findByRole('button', { name: 'traffic-analysis', pressed: true }) // 等数据渲染（medium 实心）
    const simpleRow = screen.getByTestId('complexity-row-simple')
    const simpleGroup = within(simpleRow)
    const chip = simpleGroup.getByRole('button', { name: 'traffic-analysis' })
    await userEvent.click(chip)
    expect(await screen.findByText(/移档炸了/)).toBeTruthy()
    expect(alertSpy).not.toHaveBeenCalled()
    // 回滚：traffic-analysis 回到 medium 档（medium 行实心胶囊恢复），simple 行恢复幽灵。
    const mediumRow = screen.getByTestId('complexity-row-medium')
    expect(within(mediumRow).getByRole('button', { name: 'traffic-analysis', pressed: true })).toBeTruthy()
    expect(simpleGroup.getByRole('button', { name: 'traffic-analysis', pressed: false })).toBeTruthy()
  })

  it('已指派档的 select 反映当前 provider', async () => {
    mGetRouting.mockResolvedValue(routing())
    renderView()
    const sel = (await screen.findByLabelText('标准推理 绑定部署')) as HTMLSelectElement
    expect(sel.value).toBe('deepseek')
  })

  it('改选 provider 调 saveRoleRoute(group, providerKey)', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveRole.mockResolvedValue({ role: 'complex', provider_code: 'qwen' })
    renderView()
    const sel = await screen.findByLabelText('深度推理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    await waitFor(() => expect(mSaveRole).toHaveBeenCalledWith('complex', 'qwen'))
  })

  it('能力档选空 → 删除路由（回落标准推理档）', async () => {
    mGetRouting.mockResolvedValue(routing())
    mDeleteRole.mockResolvedValue(undefined)
    renderView()
    const sel = await screen.findByLabelText('深度推理 绑定部署')
    await userEvent.selectOptions(sel, '')
    await waitFor(() => expect(mDeleteRole).toHaveBeenCalledWith('complex'))
  })

  it('指派指向缺失部署时告警「部署缺失」', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'complex', provider_code: 'ghost' }] }))
    renderView()
    expect(await screen.findByText('部署缺失')).toBeTruthy()
  })

  it('指派指向停用部署时告警「部署停用」', async () => {
    mListProv.mockResolvedValue([prov('deepseek', false)])
    mGetRouting.mockResolvedValue(routing())
    renderView()
    expect(await screen.findByText('部署停用')).toBeTruthy()
  })

  it('保存失败乐观回滚并页面内回显原因（无 window.alert）', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    mGetRouting.mockResolvedValue(routing())
    mSaveRole.mockRejectedValue(new Error('provider_code 不存在，请先创建对应 provider'))
    renderView()
    const sel = await screen.findByLabelText('标准推理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    expect(await screen.findByText(/provider_code 不存在/)).toBeTruthy()
    expect(alertSpy).not.toHaveBeenCalled()
    // 回滚：select 恢复 deepseek。
    await waitFor(() => expect((sel as HTMLSelectElement).value).toBe('deepseek'))
  })

  it('DB 里的自定义档也列出（不丢数据）', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'my-custom-group', provider_code: 'deepseek' }] }))
    renderView()
    // 自定义档 label 回退为原样 group，故 label span 与 code 同字面 → 两处命中。
    expect((await screen.findAllByText('my-custom-group')).length).toBeGreaterThan(0)
  })

  it('加载失败显示错误', async () => {
    mGetRouting.mockRejectedValue(new Error('boom'))
    renderView()
    expect(await screen.findByText(/boom/)).toBeTruthy()
  })
})
