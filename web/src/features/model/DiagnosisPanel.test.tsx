import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import type { Diagnosis, ProviderConfig } from '@/api/types'
import { diagnoseProvider } from '@/api/models'
import { DiagnosisPanel } from './DiagnosisPanel'
import { blankProvider } from './providerRules'

vi.mock('@/api/models', () => ({ diagnoseProvider: vi.fn() }))
const mDiagnose = diagnoseProvider as unknown as ReturnType<typeof vi.fn>

const steps = (status: 'ok' | 'fail'): Diagnosis['steps'] => [
  { key: 'endpoint', status, detail: '', request: 'POST https://relay.example.com/v1/chat/completions', latency_ms: 120 },
  { key: 'auth', status: 'ok', detail: 'Authorization: Bearer' },
  { key: 'models', status: 'skip', detail: '' },
  { key: 'model', status: 'ok', detail: 'm' },
  { key: 'tools', status: 'ok', detail: '支持' },
]

// 受控外壳：onApply 真正回写 draft，暴露当前 draft 供断言。
function Harness({ initial, onDraft }: { initial: ProviderConfig; onDraft: (d: ProviderConfig) => void }) {
  const [d, setD] = useState(initial)
  onDraft(d)
  return (
    <>
      <input aria-label="模型" value={d.default_model} onChange={(e) => setD({ ...d, default_model: e.target.value })} />
      <DiagnosisPanel draft={d} onApply={(p) => setD((cur) => ({ ...cur, ...p }))} onModels={() => {}} />
    </>
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

describe('DiagnosisPanel', () => {
  beforeEach(() => mDiagnose.mockReset())

  it('打通且无建议：自动回填请求画像与归一后的地址，并展示检查项与实际请求', async () => {
    mDiagnose.mockResolvedValue({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1', models: [], models_supported: false,
      profile: { auth_scheme: 'x-api-key', output_limit_param: 'max_completion_tokens', supports_tools: true },
    } satisfies Diagnosis)
    let current = base()
    render(<Harness initial={base()} onDraft={(d) => (current = d)} />)
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))

    expect(await screen.findByText('配置可用')).toBeTruthy()
    expect(screen.getByText(/POST https:\/\/relay\.example\.com\/v1\/chat\/completions/)).toBeTruthy()
    expect(current.auth_scheme).toBe('x-api-key')
    expect(current.output_limit_param).toBe('max_completion_tokens')
    expect(current.supports_tools).toBe(true)
    expect(current.base_url).toBe('https://relay.example.com/v1')
    expect(screen.queryByText(/配置已改动/)).toBeNull()
  })

  it('有建议：不擅自改写协议/地址，采纳后才回填建议与画像', async () => {
    mDiagnose.mockResolvedValue({
      ok: false, steps: steps('fail'), base_url: 'https://relay.example.com/anthropic', models: [], models_supported: false,
      profile: null,
      suggestion: { type: 'anthropic', base_url: 'https://relay.example.com/anthropic', auth_scheme: 'x-api-key', reason: '该地址按 Anthropic 协议可用' },
    } satisfies Diagnosis)
    let current = base()
    render(<Harness initial={{ ...base(), base_url: 'https://relay.example.com/anthropic' }} onDraft={(d) => (current = d)} />)
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))

    expect(await screen.findByText(/建议：该地址按 Anthropic 协议可用/)).toBeTruthy()
    expect(current.type).toBe('openai_compat')
    await userEvent.click(screen.getByRole('button', { name: '采纳' }))
    expect(current.type).toBe('anthropic')
    expect(current.auth_scheme).toBe('x-api-key')
    expect(screen.queryByRole('button', { name: '采纳' })).toBeNull()
  })

  it('未填模型：配置可用时回填自动选用的模型，且不判为过期；不可用时不回填', async () => {
    mDiagnose.mockResolvedValueOnce({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1', models: ['m-a', 'm-b'], models_supported: true,
      profile: { auth_scheme: 'bearer' }, auto_model: 'm-a',
    } satisfies Diagnosis)
    let current = base()
    render(<Harness initial={{ ...base(), default_model: '' }} onDraft={(d) => (current = d)} />)
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))
    await screen.findByText('配置可用')
    expect(current.default_model).toBe('m-a')
    expect(screen.queryByText(/配置已改动/)).toBeNull()

    mDiagnose.mockResolvedValueOnce({
      ok: false, steps: steps('ok'), base_url: 'https://relay.example.com/v1', models: ['m-a', 'm-b'], models_supported: true,
      profile: { auth_scheme: 'bearer' }, auto_model: 'm-a',
    } satisfies Diagnosis)
    await userEvent.clear(screen.getByLabelText('模型'))
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))
    await screen.findByText('未通过')
    expect(current.default_model).toBe('')
  })

  it('诊断后改动配置：结果标记为过期', async () => {
    mDiagnose.mockResolvedValue({
      ok: true, steps: steps('ok'), base_url: 'https://relay.example.com/v1', models: [], models_supported: false,
      profile: { auth_scheme: 'bearer' },
    } satisfies Diagnosis)
    render(<Harness initial={base()} onDraft={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))
    await screen.findByText('配置可用')
    await userEvent.type(screen.getByLabelText('模型'), 'x')
    expect(screen.getByText('配置已改动，请重新诊断')).toBeTruthy()
  })

  it('缺前置条件时点击即给原因，不发请求', async () => {
    render(<Harness initial={{ ...base(), api_key: '', key_present: false }} onDraft={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: /运行诊断/ }))
    expect(screen.getByText('请先填写 API Key')).toBeTruthy()
    expect(mDiagnose).not.toHaveBeenCalled()
  })
})
