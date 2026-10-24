import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProvidersView } from './ProvidersView'
import { listProviders, saveProvider, deleteProvider } from '@/api/models'
import type { ProviderConfig } from '@/api/types'

vi.mock('@/api/models', () => ({
  listProviders: vi.fn(),
  saveProvider: vi.fn(),
  deleteProvider: vi.fn(),
  // 详情面板挂载即引用 testProvider / listProviderModels（探测/测试连接），mock 掉避免真发请求。
  testProvider: vi.fn(),
  listProviderModels: vi.fn(),
}))

const mList = listProviders as unknown as ReturnType<typeof vi.fn>
const mSave = saveProvider as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteProvider as unknown as ReturnType<typeof vi.fn>

function prov(o: Partial<ProviderConfig> = {}): ProviderConfig {
  return {
    code: 'deepseek',
    type: 'openai_compat',
    base_url: 'https://api.deepseek.com',
    default_model: 'deepseek-chat',
    key_present: true,
    key_last4: '1234',
    max_tokens: 4096,
    supports_tools: true,
    supports_vision: false,
    context_window: 65536,
    description: '',
    sort_order: 0,
    enabled: true,
    ...o,
  }
}

function renderView() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <ProvidersView />
    </QueryClientProvider>,
  )
}

describe('ProvidersView', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
  })

  it('挂载拉取全量部署，左列表渲染，右侧显示未选中占位', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    expect(await screen.findByText('deepseek')).toBeTruthy()
    expect(await screen.findByText('qwen')).toBeTruthy()
    expect(screen.getByText('选择左侧部署查看详情，或点「+」新建')).toBeTruthy()
  })

  it('点列表某项，右侧详情面板原地展开该 provider（不遮挡左列表）', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    // 详情面板标题即 key；左列表仍同屏可见（未被模态遮罩覆盖）。
    expect(await screen.findByRole('heading', { name: 'deepseek' })).toBeTruthy()
    expect(screen.getByDisplayValue('deepseek-chat')).toBeTruthy()
  })

  it('搜索框按标识键/模型名本地过滤左列表', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    await screen.findByText('qwen')
    await userEvent.type(screen.getByLabelText('搜索 provider'), 'qwen')
    expect(screen.queryByText('deepseek')).toBeNull()
    expect(screen.getByText('qwen')).toBeTruthy()
  })

  it('点「新建」在右侧展开空白表单，标识键可编辑', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await screen.findByText('deepseek')
    await userEvent.click(screen.getByLabelText('新建 provider'))
    expect(await screen.findByRole('heading', { name: '接入 provider' })).toBeTruthy()
    const keyInput = screen.getByPlaceholderText('deepseek')
    expect(keyInput).not.toBeDisabled()
  })

  it('未改动时保存禁用；改动后出现未保存徽章、重置与可用的保存', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    const save = await screen.findByRole('button', { name: '保存' })
    expect(save).toBeDisabled()
    await userEvent.type(screen.getByLabelText('描述'), '主力部署')
    expect(await screen.findByText('未保存')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重置' })).toBeTruthy()
    expect(save).toBeEnabled()
  })

  it('重置放弃草稿恢复原值，未保存徽章消失', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.type(await screen.findByLabelText('描述'), '临时改动')
    await userEvent.click(await screen.findByRole('button', { name: '重置' }))
    expect(screen.queryByText('未保存')).toBeNull()
    expect((screen.getByLabelText('描述') as HTMLInputElement).value).toBe('')
  })

  it('dirty 时点其他部署被拦截，确认后才切换', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.type(await screen.findByLabelText('描述'), '改动')
    await userEvent.click(await screen.findByText('qwen'))
    // 确认条出现，详情仍是 deepseek。
    expect(await screen.findByText(/将丢弃这些更改/)).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'deepseek' })).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '丢弃并切换' }))
    expect(await screen.findByRole('heading', { name: 'qwen' })).toBeTruthy()
  })

  it('保存成功后用返回值更新详情（key_present 等服务端字段）', async () => {
    mList.mockResolvedValue([prov({ code: 'deepseek', key_present: false })])
    mSave.mockResolvedValue(prov({ code: 'deepseek', key_present: true }))
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    expect(await screen.findByText('密钥未注入')).toBeTruthy()
    await userEvent.type(screen.getByLabelText('描述'), 'x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText('密钥未注入')).toBeNull())
  })

  it('保存失败在面板内回显错误（无 window.alert）', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    mList.mockResolvedValue([prov()])
    mSave.mockRejectedValue(new Error('key 已存在'))
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.type(screen.getByLabelText('描述'), 'x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('key 已存在')).toBeTruthy()
    expect(alertSpy).not.toHaveBeenCalled()
  })

  it('删除两段式确认：首击亮确认，再击才真正删除', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockImplementation(() => true)
    mList.mockResolvedValue([prov()])
    mDelete.mockResolvedValue(undefined)
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    expect(mDelete).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: '确认删除？' }))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('deepseek'))
    expect(confirmSpy).not.toHaveBeenCalled()
    await waitFor(() =>
      expect(screen.getByText('选择左侧部署查看详情，或点「+」新建')).toBeTruthy(),
    )
  })

  it('加载失败在左列表显示错误并提供重试', async () => {
    mList.mockRejectedValue(new Error('boom'))
    renderView()
    expect(await screen.findByText(/boom/)).toBeTruthy()
    mList.mockResolvedValue([prov()])
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('deepseek')).toBeTruthy()
  })

  it('单页表单同屏可见连接与能力字段（无二级 tab）', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    // 无需点 tab，context_window（能力组）与 base_url（连接组）同屏可见。
    expect(await screen.findByDisplayValue('65536')).toBeTruthy()
    expect(screen.getByDisplayValue('https://api.deepseek.com')).toBeTruthy()
  })

  it('编辑已有密钥的 provider：默认收起展示脱敏尾号 + 更换密钥入口', async () => {
    mList.mockResolvedValue([prov({ code: 'deepseek', key_present: true, key_last4: '9f2c' })])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    expect(await screen.findByText(/9f2c/)).toBeTruthy()
    expect(screen.getByText('更换密钥')).toBeTruthy()
  })

  it('能力标志为开关控件（Switch role=switch），切换即改草稿', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    const vision = screen.getByRole('switch', { name: '支持视觉' })
    expect(vision.getAttribute('data-state')).toBe('unchecked')
    await userEvent.click(vision)
    expect(vision.getAttribute('data-state')).toBe('checked')
    expect(await screen.findByText('未保存')).toBeTruthy()
  })
})
