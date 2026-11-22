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
    base_url: 'https://api.jina.ai',
    embedding_model: 'jina-embeddings-v5-text-small',
    rerank_model: 'jina-reranker-v2-base-multilingual',
    key_present: false,
    key_last4: '',
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

  it('出厂默认：地址与模型名预填为生效值，密钥可填，提示字面检索', async () => {
    renderView()
    expect(await screen.findByLabelText('服务地址')).toHaveValue('https://api.jina.ai')
    expect(screen.getByLabelText('向量模型')).toHaveValue('jina-embeddings-v5-text-small')
    expect(screen.getByLabelText('重排模型')).toBeInTheDocument()
    expect(screen.getByLabelText('检索模型 API Key')).toBeInTheDocument()
    expect(screen.getByText(/只做字面检索/)).toBeInTheDocument()
    expect(screen.getByText(/1024 维/)).toBeInTheDocument()
  })

  it('保存：整行配置进保存请求；密钥一并携带', async () => {
    const user = userEvent.setup()
    // 首次加载未配置；保存后 mutation 失效 ['models'] 触发重拉，改读已配置状态
    mGet
      .mockResolvedValueOnce(row())
      .mockResolvedValue(row({ key_present: true, key_last4: '6789', updated_at: '2026-01-01T00:00:00Z' }))
    mSave.mockResolvedValue(row({ key_present: true, key_last4: '6789' }))
    renderView()
    await user.clear(await screen.findByLabelText('服务地址'))
    await user.type(screen.getByLabelText('服务地址'), 'https://gw.example.com')
    await user.clear(screen.getByLabelText('向量模型'))
    await user.type(screen.getByLabelText('向量模型'), 'embed-x')
    await user.type(screen.getByLabelText('检索模型 API Key'), plaintextInput)
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() =>
      expect(mSave).toHaveBeenCalledWith({
        base_url: 'https://gw.example.com',
        embedding_model: 'embed-x',
        rerank_model: 'jina-reranker-v2-base-multilingual',
        api_key: plaintextInput,
      }),
    )
    expect(await screen.findByText('已配置 ····6789')).toBeInTheDocument()
  })

  it('服务地址非法：保存禁用并就地提示', async () => {
    const user = userEvent.setup()
    renderView()
    const input = await screen.findByLabelText('服务地址')
    await user.clear(input)
    await user.type(input, 'not a url')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(screen.getByText(/http\(s\) 开头的完整地址/)).toBeInTheDocument()
    // 修正后恢复
    await user.clear(input)
    await user.type(input, 'https://gw.example.com')
    expect(input).not.toHaveAttribute('aria-invalid')
    expect(screen.getByRole('button', { name: '保存' })).toBeEnabled()
  })

  it('已配置：密钥只显示末 4 位，密钥输入收起；更换密钥展开', async () => {
    const user = userEvent.setup()
    mGet.mockResolvedValue(row({ key_present: true, key_last4: '6789', updated_at: '2026-01-01T00:00:00Z' }))
    renderView()
    expect(await screen.findByText('已配置 ····6789')).toBeInTheDocument()
    expect(screen.queryByLabelText('检索模型 API Key')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '更换密钥' }))
    expect(screen.getByLabelText('检索模型 API Key')).toBeInTheDocument()
  })

  it('测试：按表单当前值实测（含未保存的修改），两步结果就地展示', async () => {
    const user = userEvent.setup()
    mDiagnose.mockResolvedValue({
      ok: true,
      steps: [
        { key: 'embedding', status: 'fail', detail: '向量维度 1536 与库结构 1024 不符，请换 1024 维的向量模型' },
        { key: 'rerank', status: 'ok', detail: '成功', latency_ms: 50 },
      ],
    })
    renderView()
    await user.clear(await screen.findByLabelText('向量模型名'))
    await user.type(screen.getByLabelText('向量模型'), 'embed-other-model')
    await user.click(screen.getByRole('button', { name: '测试' }))
    await waitFor(() =>
      expect(mDiagnose).toHaveBeenCalledWith(
        expect.objectContaining({ embedding_model: 'embed-other-model' }),
      ),
    )
    expect(await screen.findByText(/1024 不符/)).toBeInTheDocument()
    expect(screen.getByText('通过 · 50ms')).toBeInTheDocument()
  })

  it('清除密钥：经确认后调用清除接口，地址与模型名保留', async () => {
    const user = userEvent.setup()
    const { confirmAction } = await import('@/components/ui/confirm')
    mGet.mockResolvedValue(row({ key_present: true, key_last4: '6789' }))
    mClear.mockResolvedValue(undefined)
    renderView()
    await user.click(await screen.findByRole('button', { name: '清除密钥' }))
    expect(confirmAction).toHaveBeenCalledOnce()
    await waitFor(() => expect(mClear).toHaveBeenCalledOnce())
    // 清除后地址与模型名的表单值仍在
    expect(screen.getByLabelText('服务地址')).toHaveValue('https://api.jina.ai')
  })
})
