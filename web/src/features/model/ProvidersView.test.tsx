import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProvidersView } from './ProvidersView'
import { listProviders, saveProvider, deleteProvider, restoreProviderEnvKey } from '@/api/models'
import type { ProviderConfig } from '@/api/types'

vi.mock('@/api/models', () => ({
  listProviders: vi.fn(),
  saveProvider: vi.fn(),
  deleteProvider: vi.fn(),
  restoreProviderEnvKey: vi.fn(),
  // 编辑面板挂载即引用 testProvider / listProviderModels（探测/测试连接），mock 掉避免真发请求。
  testProvider: vi.fn(),
  listProviderModels: vi.fn(),
}))

const mList = listProviders as unknown as ReturnType<typeof vi.fn>
const mSave = saveProvider as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteProvider as unknown as ReturnType<typeof vi.fn>
const mRestore = restoreProviderEnvKey as unknown as ReturnType<typeof vi.fn>

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

// 点行 → 该行下方下拉展开编辑面板（无弹窗）。
async function openEdit(o: Partial<ProviderConfig> = {}) {
  mList.mockResolvedValue([prov(o)])
  renderView()
  await userEvent.click(await screen.findByText('deepseek'))
}

describe('ProvidersView（模型提供商分区，行内下拉展开编辑）', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
  })

  it('挂载拉取全量部署，表格行渲染 code/协议/模型', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    expect(await screen.findByText('deepseek')).toBeTruthy()
    expect(await screen.findByText('qwen')).toBeTruthy()
    // 分区头：标题 + 新建入口
    expect(screen.getByText('模型提供商')).toBeTruthy()
    expect(screen.getByRole('button', { name: '新建' })).toBeTruthy()
    // 未展开时无编辑表单
    expect(screen.queryByLabelText('描述')).toBeNull()
  })

  it('搜索框按标识键/模型名过滤列表', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    await screen.findByText('qwen')
    await userEvent.type(screen.getByLabelText('搜索 provider'), 'qwen')
    expect(screen.queryByText('deepseek')).toBeNull()
    expect(screen.getByText('qwen')).toBeTruthy()
  })

  it('点行在下拉展开的编辑面板渲染单页表单（连接与能力同屏），再点行收起', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    // 无需分页，base_url（连接组）与 context_window（能力组）同屏。
    expect(await screen.findByDisplayValue('https://api.deepseek.com')).toBeTruthy()
    expect(screen.getByDisplayValue('65536')).toBeTruthy()
    // 展开行 chevron 转 down（aria-expanded=true）
    expect(screen.getByRole('button', { name: /^deepseek/ }).getAttribute('aria-expanded')).toBe('true')
    // 再点行头 → 收起
    await userEvent.click(screen.getByText('deepseek'))
    await waitFor(() => expect(screen.queryByLabelText('描述')).toBeNull())
  })

  it('新建：列表顶部下拉展开空白表单，标识符自动生成（无输入框）；必填未齐保存禁用', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await screen.findByText('deepseek')
    await userEvent.click(screen.getByRole('button', { name: '新建' }))
    expect(await screen.findByRole('heading', { name: '新建 provider' })).toBeTruthy()
    // 标识符不在 UI 出现（后端生成 UUID）；创建态无输入框
    expect(screen.queryByText('标识符')).toBeNull()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('未改动保存禁用；改动后出现未保存徽章、重置与可用的保存', async () => {
    await openEdit()
    const save = screen.getByRole('button', { name: '保存' })
    expect(save).toBeDisabled()
    await userEvent.type(screen.getByLabelText('描述'), '主力部署')
    expect(await screen.findByText('未保存')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重置' })).toBeTruthy()
    expect(save).toBeEnabled()
  })

  it('重置放弃草稿恢复原值，未保存徽章消失', async () => {
    await openEdit()
    await userEvent.type(screen.getByLabelText('描述'), '临时改动')
    await userEvent.click(screen.getByRole('button', { name: '重置' }))
    expect(screen.queryByText('未保存')).toBeNull()
    expect((screen.getByLabelText('描述') as HTMLInputElement).value).toBe('')
  })

  it('dirty 时收起被拦截：确认条出现，丢弃后才收起', async () => {
    await openEdit()
    await userEvent.type(screen.getByLabelText('描述'), '改动')
    await userEvent.click(screen.getByText('deepseek')) // 点行头收起
    expect(await screen.findByText(/收起将丢弃这些更改/)).toBeTruthy()
    expect(screen.getByLabelText('描述')).toBeTruthy() // 面板仍在
    await userEvent.click(screen.getByRole('button', { name: '丢弃并收起' }))
    await waitFor(() => expect(screen.queryByLabelText('描述')).toBeNull())
  })

  it('dirty 时点其他行被拦截：丢弃并切换后才展开目标行', async () => {
    mList.mockResolvedValue([prov(), prov({ code: 'qwen', default_model: 'qwen-plus' })])
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.type(await screen.findByLabelText('描述'), '改动')
    await userEvent.click(screen.getByText('qwen'))
    expect(await screen.findByText(/切换到其他行将丢弃这些更改/)).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '丢弃并切换' }))
    // deepseek 面板收起（aria-expanded=false），qwen 行处于展开态
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^deepseek/ }).getAttribute('aria-expanded')).toBe('false'),
    )
    expect(screen.getByRole('button', { name: /^qwen/ }).getAttribute('aria-expanded')).toBe('true')
  })

  it('保存成功后面板保持展开，用返回值刷新（key_present 等服务端字段）', async () => {
    // 首拉 key_present=false（行头密钥告警点），保存 invalidate 重拉后变 true。
    mList.mockResolvedValueOnce([prov({ key_present: false })]).mockResolvedValueOnce([prov({ key_present: true })])
    mSave.mockResolvedValue(prov({ key_present: true }))
    renderView()
    await userEvent.click(await screen.findByText('deepseek'))
    expect(await screen.findByLabelText('密钥未注入')).toBeTruthy()
    await userEvent.type(screen.getByLabelText('描述'), 'x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    // 面板保持展开；baseline 对齐后徽章与密钥告警消失
    await waitFor(() => expect(screen.queryByText('未保存')).toBeNull())
    await waitFor(() => expect(screen.queryByLabelText('密钥未注入')).toBeNull())
    expect(screen.getByLabelText('描述')).toBeTruthy()
  })

  it('保存失败在面板内回显错误（无 window.alert）', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    await openEdit()
    mSave.mockRejectedValue(new Error('code 已存在'))
    await userEvent.type(screen.getByLabelText('描述'), 'x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('code 已存在')).toBeTruthy()
    expect(alertSpy).not.toHaveBeenCalled()
  })

  it('行级删除两段式确认：垃圾桶首击亮确认，再击才真正删除并收起', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockImplementation(() => true)
    await openEdit()
    mDelete.mockResolvedValue(undefined)
    await userEvent.click(screen.getByLabelText('删除 provider deepseek'))
    expect(mDelete).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByLabelText('确认删除 deepseek'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('deepseek'))
    expect(confirmSpy).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByLabelText('描述')).toBeNull())
  })

  it('未展开行也可直接行级删除', async () => {
    mList.mockResolvedValue([prov()])
    mDelete.mockResolvedValue(undefined)
    renderView()
    await screen.findByText('deepseek')
    await userEvent.click(screen.getByLabelText('删除 provider deepseek'))
    await userEvent.click(await screen.findByLabelText('确认删除 deepseek'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('deepseek'))
  })

  it('测试连接缺前置时点击即给原因（不再禁用态静默拦截）', async () => {
    mList.mockResolvedValue([prov()])
    renderView()
    await screen.findByText('deepseek')
    await userEvent.click(screen.getByRole('button', { name: '新建' }))
    await userEvent.click(await screen.findByRole('button', { name: '测试连接' }))
    expect(await screen.findByText('需先填 Base URL、默认模型，并有可用密钥')).toBeTruthy()
    expect(mSave).not.toHaveBeenCalled()
  })

  it('加载失败显示错误并提供重试', async () => {
    mList.mockRejectedValue(new Error('boom'))
    renderView()
    expect(await screen.findByText(/boom/)).toBeTruthy()
    mList.mockResolvedValue([prov()])
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('deepseek')).toBeTruthy()
  })

  it('编辑已有密钥的 provider：默认收起展示脱敏尾号 + 更换密钥入口', async () => {
    await openEdit({ key_present: true, key_last4: '9f2c' })
    expect(await screen.findByText(/9f2c/)).toBeTruthy()
    expect(screen.getByText('更换密钥')).toBeTruthy()
  })

  it('恢复使用环境变量：仅密文生效且存在 env 引用时出现，两段式确认后清密文', async () => {
    mRestore.mockResolvedValue(prov({ key_present: true, key_last4: undefined, api_key_env: 'GLM_API_KEY' }))
    await openEdit({ key_present: true, key_last4: '9f2c', api_key_env: 'GLM_API_KEY' })
    // 密文生效 + env 引用存在 → 恢复按钮出现（等编辑面板挂载后用文本查询——
    // 按钮带 title 属性时 accessible name 取 title，role 查询会失配）
    await screen.findByText('编辑 deepseek')
    const btn = await screen.findByText('恢复使用环境变量')
    expect(mRestore).not.toHaveBeenCalled()
    await userEvent.click(btn)
    await userEvent.click(await screen.findByText('确认恢复？'))
    await waitFor(() => expect(mRestore).toHaveBeenCalledWith('deepseek'))
    // 密钥字段写回：尾号消失、回落 env 展示
    await waitFor(() => expect(screen.queryByText(/9f2c/)).toBeNull())
    expect(await screen.findByText(/来自环境变量（GLM_API_KEY）/)).toBeTruthy()
  })

  it('能力标志为开关控件（Switch role=switch），切换即改草稿', async () => {
    await openEdit()
    const vision = screen.getByRole('switch', { name: '支持视觉' })
    expect(vision.getAttribute('data-state')).toBe('unchecked')
    await userEvent.click(vision)
    expect(vision.getAttribute('data-state')).toBe('checked')
    expect(await screen.findByText('未保存')).toBeTruthy()
  })
})
