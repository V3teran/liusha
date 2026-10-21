import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    key: 'deepseek',
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

describe('ProvidersView', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockImplementation(() => true)
  })

  it('挂载拉取全量部署，左列表渲染，右侧显示未选中占位', async () => {
    mList.mockResolvedValue([prov(), prov({ key: 'qwen', default_model: 'qwen-plus' })])
    render(<ProvidersView />)
    expect(await screen.findByText('deepseek')).toBeTruthy()
    expect(await screen.findByText('qwen')).toBeTruthy()
    expect(screen.getByText('选择左侧部署查看详情，或点「+」新建')).toBeTruthy()
  })

  it('点列表某项，右侧详情面板原地展开该 provider（不遮挡左列表）', async () => {
    mList.mockResolvedValue([prov()])
    render(<ProvidersView />)
    await userEvent.click(await screen.findByText('deepseek'))
    // 详情面板标题即 key；左列表仍同屏可见（未被模态遮罩覆盖）。
    expect(await screen.findByRole('heading', { name: 'deepseek' })).toBeTruthy()
    expect(screen.getByDisplayValue('deepseek-chat')).toBeTruthy()
  })

  it('搜索框按标识键/模型名本地过滤左列表', async () => {
    mList.mockResolvedValue([prov(), prov({ key: 'qwen', default_model: 'qwen-plus' })])
    render(<ProvidersView />)
    await screen.findByText('qwen')
    await userEvent.type(screen.getByLabelText('搜索 provider'), 'qwen')
    expect(screen.queryByText('deepseek')).toBeNull()
    expect(screen.getByText('qwen')).toBeTruthy()
  })

  it('点「新建」在右侧展开空白表单，标识键可编辑', async () => {
    mList.mockResolvedValue([prov()])
    render(<ProvidersView />)
    await screen.findByText('deepseek')
    await userEvent.click(screen.getByLabelText('新建 provider'))
    expect(await screen.findByRole('heading', { name: '接入 provider' })).toBeTruthy()
    const keyInput = screen.getByPlaceholderText('deepseek')
    expect(keyInput).not.toBeDisabled()
  })

  it('保存成功后刷新列表并用返回值更新详情（key_present 等服务端字段）', async () => {
    mList.mockResolvedValue([prov({ key_present: false })])
    mSave.mockResolvedValue(prov({ key_present: true }))
    render(<ProvidersView />)
    await userEvent.click(await screen.findByText('deepseek'))
    expect(await screen.findByText('密钥未注入')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText('密钥未注入')).toBeNull())
  })

  it('删除前确认，确认后清空详情并刷新列表', async () => {
    mList.mockResolvedValueOnce([prov()]).mockResolvedValueOnce([])
    mDelete.mockResolvedValue(undefined)
    render(<ProvidersView />)
    await userEvent.click(await screen.findByText('deepseek'))
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    expect(window.confirm).toHaveBeenCalled()
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('deepseek'))
    await waitFor(() => expect(screen.getByText('选择左侧部署查看详情，或点「+」新建')).toBeTruthy())
  })

  it('加载失败在左列表显示错误', async () => {
    mList.mockRejectedValue(new Error('boom'))
    render(<ProvidersView />)
    expect(await screen.findByText(/boom/)).toBeTruthy()
  })

  it('单页表单同屏可见连接与能力字段（无二级 tab）', async () => {
    mList.mockResolvedValue([prov()])
    render(<ProvidersView />)
    await userEvent.click(await screen.findByText('deepseek'))
    // 无需点 tab，context_window（能力组）与 base_url（连接组）同屏可见。
    expect(await screen.findByDisplayValue('65536')).toBeTruthy()
    expect(screen.getByDisplayValue('https://api.deepseek.com')).toBeTruthy()
  })

  it('编辑已有密钥的 provider：默认收起展示脱敏尾号 + 更换密钥入口', async () => {
    mList.mockResolvedValue([prov({ key_present: true, key_last4: '9f2c' })])
    render(<ProvidersView />)
    await userEvent.click(await screen.findByText('deepseek'))
    expect(await screen.findByText(/9f2c/)).toBeTruthy()
    expect(screen.getByText('更换密钥')).toBeTruthy()
  })
})
