import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import type { Diagnosis, ProviderConfig } from '@/api/types'
import { diagnoseProvider } from '@/api/models'
import { DiagnosisPanel } from './DiagnosisPanel'
import { blankProvider } from './providerRules'

vi.mock('@/api/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/models')>()),
  diagnoseProvider: vi.fn(),
}))
const mDiagnose = diagnoseProvider as unknown as ReturnType<typeof vi.fn>

const steps = (status: 'ok' | 'fail'): Diagnosis['steps'] => [
  { key: 'endpoint', status, detail: '', request: 'POST https://relay.example.com/v1/chat/completions', latency_ms: 120 },
  { key: 'auth', status: 'ok', detail: 'Authorization: Bearer' },
  { key: 'model', status: 'ok', detail: 'm' },
  { key: 'tools', status: 'ok', detail: '支持' },
]

const profile = { auth_scheme: 'bearer', output_limit_param: 'max_tokens' } as const

// 受控外壳：onApply 真正回写 draft，暴露当前 draft 供断言。
function Harness({ initial, onDraft }: { initial: ProviderConfig; onDraft: (d: ProviderConfig) => void }) {
  const [d, setD] = useState(initial)
  onDraft(d)
  return (
    <>
      <input aria-label="模型" value={d.default_model} onChange={(e) => setD({ ...d, default_model: e.target.value })} />
      <DiagnosisPanel draft={d} onApply={(p) => setD((cur) => ({ ...cur, ...p }))} />
    </>
  )
}

function renderHarness(initial: ProviderConfig, onDraft: (d: ProviderConfig) => void = () => {}) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Harness initial={initial} onDraft={onDraft} />
    </QueryClientProvider>,
  )
}

const base = (): ProviderConfig => ({
  ...blankProvider(),
  type: 'openai_compat',
  base_url: 'https://relay.example.com/v1/',
  default_model: 'm',
  api_key: 'sk',
  supports_tools: false,
})

const testButton = () => screen.getByRole('button', { name: /测试连接/ })

describe('DiagnosisPanel', () => {
  beforeEach(() => {
    mDiagnose.mockReset()
  })

  it('打通且无建议：自动回填请求画像与归一后的地址，并展示检查项与实际请求', async () => {
    mDiagnose.mockResolvedValue({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1',
      profile: { auth_scheme: 'x-api-key', output_limit_param: 'max_completion_tokens', supports_tools: true },
    } satisfies Diagnosis)
    let current = base()
    renderHarness(base(), (d) => (current = d))
    await userEvent.click(testButton())

    expect(await screen.findByText('配置可用')).toBeTruthy()
    expect(screen.getByText(/POST https:\/\/relay\.example\.com\/v1\/chat\/completions/)).toBeTruthy()
    expect(current.auth_scheme).toBe('x-api-key')
    expect(current.output_limit_param).toBe('max_completion_tokens')
    expect(current.supports_tools).toBe(true)
    expect(current.base_url).toBe('https://relay.example.com/v1')
    expect(screen.queryByText(/配置已改动/)).toBeNull()
  })

  // 测试在途时用户又改了配置：迟到的结果只能作为过期结果展示，不得回填覆盖新输入。
  it('迟到的测试结果不覆盖期间改过的配置', async () => {
    let resolve!: (d: Diagnosis) => void
    mDiagnose.mockImplementation(() => new Promise((r) => (resolve = r)))
    let current = base()
    renderHarness(base(), (d) => (current = d))
    await userEvent.click(testButton())
    const model = screen.getByLabelText('模型')
    await userEvent.clear(model)
    await userEvent.type(model, 'm2')
    resolve({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1',
      profile: { auth_scheme: 'x-api-key', output_limit_param: 'max_completion_tokens', supports_tools: true },
    })
    expect(await screen.findByText(/配置已改动/)).toBeTruthy()
    expect(current.default_model).toBe('m2')
    expect(current.auth_scheme).not.toBe('x-api-key')
  })

  it('有建议：不擅自改写协议/地址，采纳后才回填建议与画像', async () => {
    mDiagnose.mockResolvedValue({
      ok: false, steps: steps('fail'), base_url: 'https://relay.example.com/anthropic',
      profile: null,
      suggestion: { type: 'anthropic', base_url: 'https://relay.example.com/anthropic', auth_scheme: 'x-api-key', reason: '该地址按 Anthropic 协议可用' },
    } satisfies Diagnosis)
    let current = base()
    renderHarness({ ...base(), base_url: 'https://relay.example.com/anthropic' }, (d) => (current = d))
    await userEvent.click(testButton())

    expect(await screen.findByText(/建议：该地址按 Anthropic 协议可用/)).toBeTruthy()
    expect(current.type).toBe('openai_compat')
    await userEvent.click(screen.getByRole('button', { name: '采纳' }))
    expect(current.type).toBe('anthropic')
    expect(current.auth_scheme).toBe('x-api-key')
    expect(screen.queryByRole('button', { name: '采纳' })).toBeNull()
  })

  // 可用性只能由对话请求证明：没填模型时就地说明，不发请求、不替用户挑模型。
  it('未填模型：点击即提示先填写或拉取模型，不发请求', async () => {
    renderHarness({ ...base(), default_model: '  ' })
    await userEvent.click(testButton())
    expect(screen.getByText('请先填写默认模型（可从模型列表拉取）')).toBeTruthy()
    expect(mDiagnose).not.toHaveBeenCalled()
  })

  it('按表单当前值发请求：模型去空白，已存密钥留空', async () => {
    mDiagnose.mockResolvedValue({ ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1', profile } satisfies Diagnosis)
    renderHarness({ ...base(), code: 'p1', api_key: '', key_present: true, default_model: ' m ', auth_scheme: 'x-api-key' })
    await userEvent.click(testButton())
    await screen.findByText('配置可用')
    expect(mDiagnose.mock.calls[0][0]).toMatchObject({
      code: 'p1', default_model: 'm', auth_scheme: 'x-api-key', output_limit_param: 'max_tokens',
    })
    expect(mDiagnose.mock.calls[0][0].api_key).toBeUndefined()
  })

  it('测试后改动配置：结果标记为过期', async () => {
    mDiagnose.mockResolvedValue({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1',
      profile,
    } satisfies Diagnosis)
    renderHarness(base())
    await userEvent.click(testButton())
    await screen.findByText('配置可用')
    await userEvent.type(screen.getByLabelText('模型'), 'x')
    expect(screen.getByText('配置已改动，请重新测试')).toBeTruthy()
  })

  it('缺前置条件时点击即给原因，不发请求', async () => {
    renderHarness({ ...base(), api_key: '', key_present: false })
    await userEvent.click(testButton())
    expect(screen.getByText('请先填写 API Key')).toBeTruthy()
    expect(mDiagnose).not.toHaveBeenCalled()
  })
})
