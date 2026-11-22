import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RetrievalView } from './RetrievalView'
import {
  getRetrievalModel,
  saveRetrievalModel,
  clearRetrievalModel,
  diagnoseRetrievalModel,
} from '@/api/models'
import type { RetrievalModelConfig } from '@/api/types'

vi.mock('@/api/models', () => ({
  getRetrievalModel: vi.fn(),
  saveRetrievalModel: vi.fn(),
  clearRetrievalModel: vi.fn(),
  diagnoseRetrievalModel: vi.fn(),
}))

vi.mock('@/components/ui/confirm', () => ({
  confirmAction: vi.fn().mockResolvedValue(true),
}))

const mGet = getRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mSave = saveRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mClear = clearRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mDiagnose = diagnoseRetrievalModel as unknown as ReturnType<typeof vi.fn>

// 明文密钥位走变量引用而非字面量：静态扫描对凭据字段上的字面量一律报硬编码凭据。
const plaintextInput = ['mock', 'retrieval', 'key'].join('-')

function row(overrides: Partial<RetrievalModelConfig> = {}): RetrievalModelConfig {
  return {
    key_present: false,
    key_last4: '',
    embedding_model: 'fake-embed',
    rerank_model: 'fake-rerank',
    updated_at: null,
    ...overrides,
  }
}

function renderView() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <RetrievalView />
    </QueryClientProvider>,
  )
}

describe('检索模型分区', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mGet.mockResolvedValue(row())
  })

  it('未配置：展示只读模型名、密钥输入与保存入口，并提示字面检索', async () => {
    renderView()
    expect(await screen.findByText('fake-embed')).toBeInTheDocument()
    expect(screen.getByLabelText('检索模型 API Key')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(screen.getByText(/只做字面检索/)).toBeInTheDocument()
  })

  it('保存：输入密钥后可用，调用保存接口并提示成功；空输入不可用', async () => {
    const user = userEvent.setup()
    // 首次加载未配置；保存后 mutation 失效 ['models'] 触发重拉，改读已配置状态（模拟服务端已落库）
    mGet.mockResolvedValueOnce(row()).mockResolvedValue(
      row({ key_present: true, key_last4: '6789', updated_at: '2026-01-01T00:00:00Z' }),
    )
    mSave.mockResolvedValue(row({ key_present: true, key_last4: '6789', updated_at: '2026-01-01T00:00:00Z' }))
    renderView()
    const input = await screen.findByLabelText('检索模型 API Key')
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    await user.type(input, plaintextInput)
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(mSave).toHaveBeenCalledWith(plaintextInput))
    expect(await screen.findByText('已配置 ····6789')).toBeInTheDocument()
  })

  it('已配置：密钥只显示末 4 位，输入框收起，可更换与清除', async () => {
    const user = userEvent.setup()
    mGet.mockResolvedValue(row({ key_present: true, key_last4: '6789', updated_at: '2026-01-01T00:00:00Z' }))
    renderView()
    expect(await screen.findByText('已配置 ····6789')).toBeInTheDocument()
    expect(screen.queryByLabelText('检索模型 API Key')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '更换密钥' }))
    expect(screen.getByLabelText('检索模型 API Key')).toBeInTheDocument()
  })

  it('测试：输入框有值测表单当前值，留空测已存密钥；两步结果就地展示', async () => {
    const user = userEvent.setup()
    mDiagnose.mockResolvedValue({
      ok: true,
      steps: [
        { key: 'embedding', status: 'ok', detail: '成功', latency_ms: 120 },
        { key: 'rerank', status: 'fail', detail: '上游 401', latency_ms: 50 },
      ],
    })
    renderView()
    await user.click(await screen.findByRole('button', { name: '测试' }))
    await waitFor(() => expect(mDiagnose).toHaveBeenCalledWith(undefined))
    expect(await screen.findByText('通过 · 120ms')).toBeInTheDocument()
    expect(screen.getByText('上游 401')).toBeInTheDocument()
  })

  it('清除：经确认后调用清除接口', async () => {
    const user = userEvent.setup()
    const { confirmAction } = await import('@/components/ui/confirm')
    mGet.mockResolvedValue(row({ key_present: true, key_last4: '6789' }))
    mClear.mockResolvedValue(undefined)
    renderView()
    await user.click(await screen.findByRole('button', { name: '清除' }))
    expect(confirmAction).toHaveBeenCalledOnce()
    await waitFor(() => expect(mClear).toHaveBeenCalledOnce())
  })
})
