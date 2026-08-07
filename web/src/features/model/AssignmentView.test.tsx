import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AssignmentView } from './AssignmentView'
import { listProviders, getRouting, saveRoleRoute, deleteRoleRoute } from '@/api/models'
import type { ProviderConfig, RoutingResponse } from '@/api/types'

vi.mock('@/api/models', () => ({
  listProviders: vi.fn(),
  getRouting: vi.fn(),
  saveRoleRoute: vi.fn(),
  deleteRoleRoute: vi.fn(),
}))

const mListProv = listProviders as unknown as ReturnType<typeof vi.fn>
const mGetRouting = getRouting as unknown as ReturnType<typeof vi.fn>
const mSaveRole = saveRoleRoute as unknown as ReturnType<typeof vi.fn>
const mDeleteRole = deleteRoleRoute as unknown as ReturnType<typeof vi.fn>

function prov(key: string, enabled = true): ProviderConfig {
  return {
    key,
    type: 'openai_compat',
    base_url: 'https://x',
    default_model: 'm',
    api_key_env: 'X_API_KEY',
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

const routing = (o: Partial<RoutingResponse> = {}): RoutingResponse => ({
  routes: [{ role: 'orchestrator', provider_key: 'deepseek' }],
  ...o,
})

describe('AssignmentView', () => {
  beforeEach(() => {
    mListProv.mockReset()
    mGetRouting.mockReset()
    mSaveRole.mockReset()
    mDeleteRole.mockReset()
    mListProv.mockResolvedValue([prov('deepseek'), prov('qwen')])
    vi.spyOn(window, 'alert').mockImplementation(() => {})
  })

  it('挂载并行加载 provider + 路由，渲染业务角色与兜底角色两组', async () => {
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    expect(await screen.findByText('业务角色')).toBeTruthy()
    expect(await screen.findByText('兜底角色')).toBeTruthy()
    // 已知角色中文标签与保留角色标签都出现。
    expect(await screen.findByText('编排主代理')).toBeTruthy()
    expect(await screen.findByText('默认兜底')).toBeTruthy()
    expect(await screen.findByText('重试备份')).toBeTruthy()
  })

  it('已指派角色的 select 反映当前 provider', async () => {
    mGetRouting.mockResolvedValue(routing())
    render(<AssignmentView />)
    const sel = (await screen.findByLabelText('编排主代理 绑定部署')) as HTMLSelectElement
    expect(sel.value).toBe('deepseek')
  })

  it('改选 provider 调 saveRoleRoute(role, providerKey)', async () => {
    mGetRouting.mockResolvedValue(routing())
    mSaveRole.mockResolvedValue({ role: 'orchestrator', provider_key: 'qwen' })
    render(<AssignmentView />)
    const sel = await screen.findByLabelText('编排主代理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    await waitFor(() => expect(mSaveRole).toHaveBeenCalledWith('orchestrator', 'qwen'))
  })

  it('业务角色选空 → 删除路由（回落兜底）', async () => {
    mGetRouting.mockResolvedValue(routing())
    mDeleteRole.mockResolvedValue(undefined)
    render(<AssignmentView />)
    const sel = await screen.findByLabelText('编排主代理 绑定部署')
    await userEvent.selectOptions(sel, '')
    await waitFor(() => expect(mDeleteRole).toHaveBeenCalledWith('orchestrator'))
  })

  it('指派指向缺失部署时告警「部署缺失」', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'orchestrator', provider_key: 'ghost' }] }))
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
    const sel = await screen.findByLabelText('编排主代理 绑定部署')
    await userEvent.selectOptions(sel, 'qwen')
    await waitFor(() => expect(alertSpy).toHaveBeenCalledWith('provider_key 不存在'))
  })

  it('DB 里的自定义角色也列出（不丢数据）', async () => {
    mGetRouting.mockResolvedValue(routing({ routes: [{ role: 'my-custom-role', provider_key: 'deepseek' }] }))
    render(<AssignmentView />)
    // 自定义角色 label 回退为原样 role，故 label span 与 code 同字面 → 两处命中。
    expect((await screen.findAllByText('my-custom-role')).length).toBeGreaterThan(0)
  })

  it('加载失败显示错误', async () => {
    mGetRouting.mockRejectedValue(new Error('boom'))
    render(<AssignmentView />)
    expect(await screen.findByText(/boom/)).toBeTruthy()
  })
})
