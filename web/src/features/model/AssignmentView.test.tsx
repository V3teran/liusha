import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    key,
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
    kind: 'executor',
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
  routes: [{ role: 'medium', provider_key: 'deepseek' }],
  ...o,
})

const agents = () => [
  agent('h-orch', 'planner', 'complex'),
  agent('h-traffic', 'traffic-analysis', 'medium'),
]

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
    vi.spyOn(window, 'alert').mockImplementation(() => {})
  })

  it('挂载并行加载 provider + 路由 + 智能体，渲染能力档与兜底槽两组', async () => {
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    expect(await screen.findByText('能力档')).toBeTruthy()
    expect(await screen.findByText('兜底槽')).toBeTruthy()
    // 三档中文标签与保留档标签都出现。
    expect(await screen.findByText('深度推理')).toBeTruthy()
    expect(await screen.findByText('标准推理')).toBeTruthy()
    expect(await screen.findByText('轻量快答')).toBeTruthy()
    expect(await screen.findByText('兜底部署')).toBeTruthy()
    // 内建路由键（simple 档）只读 chip。
    expect(await screen.findByText('inspector')).toBeTruthy()
    expect(await screen.findByText('compactor')).toBeTruthy()
  })

  it('智能体复选框在其所属档勾选（agent.complexity 分组）', async () => {
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    // planner 在 complex 档 → 归入 complex 的复选框勾选；归入 medium 的空勾。
    const orchComplex = (await screen.findByLabelText('planner 归入 complex')) as HTMLInputElement
    const orchMedium = (await screen.findByLabelText('planner 归入 medium')) as HTMLInputElement
    expect(orchComplex.checked).toBe(true)
    expect(orchMedium.checked).toBe(false)
  })

  it('勾选把智能体移入目标档，调 saveAgentComplexity(id, complexity)', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveTier.mockResolvedValue(agent('h-traffic', 'traffic-analysis', 'simple'))
    render(<AssignmentView />)
    // traffic-analysis 原在 medium，勾其 simple 复选框 → 移入 simple。
    const box = await screen.findByLabelText('traffic-analysis 归入 simple')
    await userEvent.click(box)
    await waitFor(() => expect(mSaveTier).toHaveBeenCalledWith('h-traffic', 'simple'))
  })

  it('移档失败回滚并弹 alert', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveTier.mockRejectedValue(new Error('移档炸了'))
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    render(<AssignmentView />)
    const box = (await screen.findByLabelText('traffic-analysis 归入 simple')) as HTMLInputElement
    await userEvent.click(box)
    await waitFor(() => expect(alertSpy).toHaveBeenCalledWith('移档炸了'))
    // 回滚：仍在 medium，simple 复选框空勾。
    await waitFor(() => expect(box.checked).toBe(false))
  })

  it('已指派档的 select 反映当前 provider', async () => {
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    const sel = (await screen.findByLabelText('标准推理 绑定部署')) as HTMLSelectElement
    expect(sel.value).toBe('deepseek')
  })

  it('改选 provider 调 saveRoleRoute(tier, providerKey)', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveRole.mockResolvedValue({ role: 'complex', provider_key: 'qwen' })
    render(<AssignmentView />)
    const sel = await screen.findByLabelText('深度推理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    await waitFor(() => expect(mSaveRole).toHaveBeenCalledWith('complex', 'qwen'))
  })

  it('能力档选空 → 删除路由（回落标准推理档）', async () => {
    mGetRouting.mockResolvedValue(routing())
    mDeleteRole.mockResolvedValue(undefined)
    render(<AssignmentView />)
    const sel = await screen.findByLabelText('深度推理 绑定部署')
    await userEvent.selectOptions(sel, '')
    await waitFor(() => expect(mDeleteRole).toHaveBeenCalledWith('complex'))
  })

  it('指派指向缺失部署时告警「部署缺失」', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'complex', provider_key: 'ghost' }] }))
    render(<AssignmentView />)
    expect(await screen.findByText('部署缺失')).toBeTruthy()
  })

  it('指派指向停用部署时告警「部署停用」', async () => {
    mListProv.mockResolvedValue([prov('deepseek', false)])
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    expect(await screen.findByText('部署停用')).toBeTruthy()
  })

  it('保存失败弹 alert（不崩）', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveRole.mockRejectedValue(new Error('provider_key 不存在'))
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    render(<AssignmentView />)
    const sel = await screen.findByLabelText('标准推理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    await waitFor(() => expect(alertSpy).toHaveBeenCalledWith('provider_key 不存在'))
  })

  it('DB 里的自定义档也列出（不丢数据）', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'my-custom-tier', provider_key: 'deepseek' }] }))
    render(<AssignmentView />)
    // 自定义档 label 回退为原样 tier，故 label span 与 code 同字面 → 两处命中。
    expect((await screen.findAllByText('my-custom-tier')).length).toBeGreaterThan(0)
  })

  it('加载失败显示错误', async () => {
    mGetRouting.mockRejectedValue(new Error('boom'))
    render(<AssignmentView />)
    expect(await screen.findByText(/boom/)).toBeTruthy()
  })
})
