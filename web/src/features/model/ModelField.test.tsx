import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import type { ProviderConfig, ProviderModelList } from '@/api/types'
import { listProviderModels } from '@/api/models'
import { ModelField } from './ModelField'
import { blankProvider } from './providerRules'

vi.mock('@/api/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/models')>()),
  listProviderModels: vi.fn(),
}))
const mList = listProviderModels as unknown as ReturnType<typeof vi.fn>

const base = (): ProviderConfig => ({
  ...blankProvider(),
  type: 'openai_chat_completions',
  base_url: 'https://relay.example.com/v1',
  api_key: 'sk',
})

// 受控外壳：字段改动真正回写 draft；另给一个 Base URL 输入，用来改动连接。
function Harness({ initial, onDraft }: { initial: ProviderConfig; onDraft: (d: ProviderConfig) => void }) {
  const [d, setD] = useState(initial)
  onDraft(d)
  return (
    <>
      <input aria-label="Base URL" value={d.base_url} onChange={(e) => setD({ ...d, base_url: e.target.value })} />
      <ModelField draft={d} patch={(p) => setD((cur) => ({ ...cur, ...p }))} />
    </>
  )
}

function renderField(initial: ProviderConfig = base(), onDraft: (d: ProviderConfig) => void = () => {}) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Harness initial={initial} onDraft={onDraft} />
    </QueryClientProvider>,
  )
}

const fetchButton = () => screen.getByRole('button', { name: '拉取模型列表' })

describe('ModelField', () => {
  beforeEach(() => {
    mList.mockReset()
  })

  it('拉取成功：展开下拉列出全部模型，选中即填入；不改写其它字段', async () => {
    mList.mockResolvedValue({
      models: ['m-a', 'm-b'], status: 'ok', request: 'GET https://relay.example.com/v1/models',
    } satisfies ProviderModelList)
    let current = base()
    const initial = { ...base(), code: 'p1', auth_scheme: 'x-api-key' as const }
    renderField(initial, (d) => (current = d))
    await userEvent.click(fetchButton())

    expect(await screen.findByRole('option', { name: 'm-b' })).toBeTruthy()
    expect(screen.getByText('已拉取 2 个模型')).toBeTruthy()
    expect(mList.mock.calls[0][0]).toEqual({
      code: 'p1', type: 'openai_chat_completions', base_url: 'https://relay.example.com/v1', api_key: 'sk', auth_scheme: 'x-api-key',
    })
    expect(current).toEqual(initial)

    await userEvent.click(screen.getByRole('option', { name: 'm-b' }))
    expect(current.default_model).toBe('m-b')
  })

  // 禁用会让已聚焦的按钮失焦（浏览器行为），响应回来时就无从判断用户是否已转去别处。
  it('拉取期间按钮保持可用、焦点不丢；重复点击不重复请求', async () => {
    let resolve!: (l: ProviderModelList) => void
    mList.mockImplementation(() => new Promise((r) => (resolve = r)))
    renderField()
    await userEvent.click(fetchButton())
    await userEvent.click(fetchButton())
    expect(fetchButton().hasAttribute('disabled')).toBe(false)
    expect(fetchButton().getAttribute('aria-busy')).toBe('true')
    expect(document.activeElement).toBe(fetchButton())
    expect(mList).toHaveBeenCalledTimes(1)
    resolve({ models: ['m-a'], status: 'ok', request: 'GET https://relay.example.com/v1/models' })
    expect(await screen.findByRole('option', { name: 'm-a' })).toBeTruthy()
  })

  it('端点不提供列表：如实提示并带上实际请求，可直接手填', async () => {
    mList.mockResolvedValue({
      models: [], status: 'warn', detail: '模型列表接口返回 404：该端点可能不提供模型列表', request: 'GET https://relay.example.com/v1/models',
    } satisfies ProviderModelList)
    renderField()
    await userEvent.click(fetchButton())
    expect(await screen.findByText(/该端点可能不提供模型列表/)).toBeTruthy()
    expect(screen.getByText('GET https://relay.example.com/v1/models')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '展开模型列表' })).toBeNull()
  })

  it('拉取失败：就地给出原因', async () => {
    mList.mockResolvedValue({
      models: [], status: 'fail', detail: '鉴权失败：API Key 无效（401）', request: 'GET https://relay.example.com/v1/models',
    } satisfies ProviderModelList)
    renderField()
    await userEvent.click(fetchButton())
    expect((await screen.findByRole('alert')).textContent).toContain('鉴权失败')
  })

  it('请求无法发起（后端拒绝）：显示后端给出的原因', async () => {
    mList.mockRejectedValue(new Error('取已存 provider 失败'))
    renderField()
    await userEvent.click(fetchButton())
    expect((await screen.findByRole('alert')).textContent).toContain('取已存 provider 失败')
  })

  it('缺前置条件时点击即给原因，不发请求；补齐后提示随之消失', async () => {
    renderField({ ...base(), base_url: '' })
    await userEvent.click(fetchButton())
    expect(screen.getByText('请先填写完整的 Base URL')).toBeTruthy()
    expect(mList).not.toHaveBeenCalled()
    await userEvent.type(screen.getByLabelText('Base URL'), 'https://relay.example.com/v1')
    expect(screen.queryByText('请先填写完整的 Base URL')).toBeNull()
  })

  it('连接改动后，旧连接拉到的模型不再作为选项', async () => {
    mList.mockResolvedValue({ models: ['m-a'], status: 'ok', request: 'GET https://relay.example.com/v1/models' } satisfies ProviderModelList)
    renderField()
    await userEvent.click(fetchButton())
    await screen.findByText('已拉取 1 个模型')
    await userEvent.type(screen.getByLabelText('Base URL'), '/other')
    expect(screen.queryByText('已拉取 1 个模型')).toBeNull()
    expect(screen.queryByRole('button', { name: '展开模型列表' })).toBeNull()
  })
})
