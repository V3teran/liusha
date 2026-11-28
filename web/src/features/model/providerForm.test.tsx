import { describe, expect, it, vi } from 'vitest'
import { useState, type ReactNode } from 'react'
import { render as rtlRender, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { ProviderFormBody } from './providerForm'
import {
  baseUrlError,
  blankProvider,
  chatRequestUrl,
  normalizeBaseUrl,
  providerInvalid,
  thinkingBudgetError,
} from './providerRules'
import type { ProviderConfig } from '@/api/types'

// 数字字段契约：用户可清空（不回写 0）、可输入任意前导 0（显示忠实于键入），
// 变更实时回写 draft 数值。此前 onChange 直接 Number(e.target.value)：
// Number("")=0 让「清空」变成「填 0」，受控回写让字段卡着删不掉的 0。
const draft = (over: Partial<ProviderConfig> = {}): ProviderConfig => ({
  ...blankProvider(),
  display_name: '主力',
  type: 'openai_chat_completions',
  base_url: 'https://x.example',
  default_model: 'm1',
  api_key: 'k',
  ...over,
})

// 表单里的模型字段带「拉取模型列表」（TanStack mutation），渲染需要 QueryClient。
function render(ui: ReactNode) {
  const client = new QueryClient()
  return rtlRender(ui, { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
}

function renderForm(over: Partial<ProviderConfig> = {}, patchSpy = vi.fn()) {
  render(
    <ProviderFormBody
      draft={draft(over)}
      patch={patchSpy}
      isNew
    />,
  )
  return { patchSpy }
}

describe('providerForm 数字字段', () => {
  it('上下文窗口：清空字段 → 不回写 0，draft 收到可区分的空态', async () => {
    const user = userEvent.setup()
    const { patchSpy } = renderForm()
    const input = screen.getByLabelText(/上下文窗口/)
    await user.clear(input)
    expect((input as HTMLInputElement).value).toBe('') // DOM 不被回写成 "0"
    const calls = patchSpy.mock.calls.map((c) => c[0].context_window)
    expect(calls[calls.length - 1]).toBe('') // 空串透传给 draft（由保存校验拦截），而非 0
  })

  it('上下文窗口：输入前导 0 → 显示忠实键入（不丢前导 0），draft 数值正确', async () => {
    const user = userEvent.setup()
    const { patchSpy } = renderForm()
    const input = screen.getByLabelText(/上下文窗口/)
    await user.clear(input)
    await user.type(input, '0128000')
    // 真实浏览器（Chrome 实测）DOM 保留键入的 "0128000"；jsdom 会把 number input
    // 值规范化为 "128000"。契约核心：React 不再从数字回写 DOM（吞前导 0 的旧机制已除），
    // 且 draft 收到正确数值。
    expect(['0128000', '128000']).toContain((input as HTMLInputElement).value)
    const calls = patchSpy.mock.calls.map((c) => c[0].context_window)
    expect(calls[calls.length - 1]).toBe(128000) // draft 收到数值
  })

  it('单次最大输出 tokens：清空不回写 0，输入回写数值', async () => {
    const user = userEvent.setup()
    const { patchSpy } = renderForm()
    const input = screen.getByLabelText(/单次最大输出/)
    await user.clear(input)
    expect((input as HTMLInputElement).value).toBe('')
    await user.type(input, '08192')
    expect(['08192', '8192']).toContain((input as HTMLInputElement).value) // 同上：jsdom 规范化
    const calls = patchSpy.mock.calls.map((c) => c[0].max_tokens)
    expect(calls[calls.length - 1]).toBe(8192)
  })

  it('重置场景：外部 draft 值变化时本地显示跟随（重置按钮依赖）', async () => {
    const user = userEvent.setup()
    const { rerender } = render(
      <ProviderFormBody draft={draft()} patch={vi.fn()} isNew />,
    )
    const input = screen.getByLabelText(/上下文窗口/)
    await user.clear(input)
    await user.type(input, '123')
    rerender(
      <ProviderFormBody draft={draft({ context_window: 999999 })} patch={vi.fn()} isNew />,
    )
    expect((input as HTMLInputElement).value).toBe('999999')
  })
})

describe('上下文窗口与输出上限的缺省', () => {
  it('新建的初值就是缺省，与后端唯一的出处同值：取当前旗舰模型的官方上限', () => {
    expect(blankProvider().max_tokens).toBe(131072)
    expect(blankProvider().context_window).toBe(1048576)
  })
})

describe('providerInvalid', () => {
  it('context_window 为空串/0 时表单无效（保存禁用），>0 时有效', () => {
    expect(providerInvalid(draft({ context_window: '' as unknown as number }), true)).toBe(true)
    expect(providerInvalid(draft({ context_window: 0 }), true)).toBe(true)
    expect(providerInvalid(draft({ context_window: 1 }), true)).toBe(false)
  })

  it('显示名称必填（空白视为未填）', () => {
    expect(providerInvalid(draft({ display_name: '  ' }), true)).toBe(true)
    expect(providerInvalid(draft({ display_name: 'DeepSeek' }), true)).toBe(false)
  })
})

describe('providerForm 校验（与后端 llmconfig.Validate 同口径）', () => {
  it('Base URL 须为完整 http(s) 地址', () => {
    expect(baseUrlError('https://api.example.com/v1')).toBeUndefined()
    expect(baseUrlError('')).toBeUndefined() // 空值由必填校验兜
    expect(baseUrlError('api.example.com')).toBeTruthy()
    expect(baseUrlError('ftp://x')).toBeTruthy()
    expect(providerInvalid(draft({ base_url: 'u' }), true)).toBe(true)
  })

  it('扩展思考预算：仅 Anthropic Messages 生效，开启时 ≥1024 且小于 max_tokens（没填按缺省 131072，与后端同口径）', () => {
    const a = (budget: number | '', maxTokens: number | '' = 8192) =>
      draft({ type: 'anthropic_messages', thinking_budget_tokens: budget, max_tokens: maxTokens })
    expect(thinkingBudgetError(a(0))).toBeUndefined()
    expect(thinkingBudgetError(a(4096))).toBeUndefined()
    expect(thinkingBudgetError(a(512))).toBeTruthy()
    expect(thinkingBudgetError(a(8192))).toBeTruthy()
    expect(thinkingBudgetError(a(65536, 0))).toBeUndefined()
    expect(thinkingBudgetError(a(131072, 0))).toBeTruthy()
    expect(thinkingBudgetError(a(131072, ''))).toBeTruthy()
    expect(thinkingBudgetError(draft({ type: 'openai_chat_completions', thinking_budget_tokens: 512 }))).toBeUndefined()
    expect(providerInvalid(a(512), true)).toBe(true)
    expect(providerInvalid(a(4096), true)).toBe(false)
  })

  it('思考预算字段只在 Anthropic Messages 协议下出现', () => {
    renderForm({ type: 'openai_chat_completions' })
    expect(screen.queryByLabelText(/扩展思考预算/)).toBeNull()
  })

  it('Anthropic 协议下可编辑思考预算并就地提示违规', async () => {
    const user = userEvent.setup()
    const { patchSpy } = renderForm({ type: 'anthropic_messages', thinking_budget_tokens: 512, max_tokens: 8192 })
    expect(screen.getByRole('alert').textContent).toContain('至少 1024')
    const input = screen.getByLabelText(/扩展思考预算/)
    await user.clear(input)
    await user.type(input, '2048')
    const calls = patchSpy.mock.calls.map((c) => c[0].thinking_budget_tokens).filter((v) => v !== undefined)
    expect(calls[calls.length - 1]).toBe(2048)
  })
})

describe('协议与 Base URL（只认协议规则，不认具体服务）', () => {
  it('地址归一与后端同口径：去尾斜杠、去误粘的操作路径；Anthropic 再去末尾 /v1', () => {
    expect(normalizeBaseUrl('openai_chat_completions', ' https://api.example.com/v1/ ')).toBe('https://api.example.com/v1')
    expect(normalizeBaseUrl('openai_chat_completions', 'https://api.example.com/v1/chat/completions')).toBe('https://api.example.com/v1')
    expect(normalizeBaseUrl('anthropic_messages', 'https://api.example.com/anthropic/v1/messages')).toBe('https://api.example.com/anthropic')
    expect(normalizeBaseUrl('anthropic_messages', 'https://api.example.com/v1')).toBe('https://api.example.com')
    expect(normalizeBaseUrl('openai_chat_completions', 'not a url')).toBe('not a url')
  })

  it('实际请求地址与 SDK 拼接规则一致', () => {
    expect(chatRequestUrl('openai_chat_completions', 'https://api.example.com/v1')).toBe('https://api.example.com/v1/chat/completions')
    expect(chatRequestUrl('anthropic_messages', 'https://api.example.com/anthropic')).toBe('https://api.example.com/anthropic/v1/messages')
  })

  it('未选协议时表单无效', () => {
    expect(providerInvalid({ ...draft(), type: '' }, true)).toBe(true)
  })

  // 受控外壳：让 patch 真正回写 draft，验证表单交互。
  function Harness({ initial }: { initial: ProviderConfig }) {
    const [d, setD] = useState<ProviderConfig>(initial)
    return <ProviderFormBody draft={d} patch={(p) => setD((cur) => ({ ...cur, ...p }))} isNew />
  }

  it('新建时先选协议：未选只展示协议，选定后展开其余字段并取该协议的标准鉴权方式', async () => {
    const user = userEvent.setup()
    render(<Harness initial={{ ...blankProvider(), api_key: 'k' }} />)
    expect(screen.queryByText('Base URL')).toBeNull()
    await user.click(screen.getByRole('radio', { name: /Anthropic/ }))
    expect(screen.getByRole('radio', { name: /Anthropic/ }).getAttribute('aria-checked')).toBe('true')
    expect((screen.getByLabelText(/鉴权方式/) as HTMLSelectElement).value).toBe('x-api-key')
    await user.click(screen.getByRole('radio', { name: 'OpenAI Chat Completions' }))
    expect((screen.getByLabelText(/鉴权方式/) as HTMLSelectElement).value).toBe('bearer')
  })

  it('Base URL 下方实时预览归一后的实际请求地址', async () => {
    const user = userEvent.setup()
    render(<Harness initial={{ ...draft(), type: 'anthropic_messages', base_url: '' }} />)
    await user.type(screen.getByPlaceholderText('https://api.example.com/anthropic'), 'https://api.example.com/anthropic/v1/messages')
    expect(screen.getByText('实际请求：POST https://api.example.com/anthropic/v1/messages')).toBeTruthy()
  })

  it('输出上限字段只在 OpenAI Chat Completions 协议下出现', () => {
    render(<Harness initial={{ ...draft(), type: 'anthropic_messages' }} />)
    expect(screen.queryByLabelText(/输出上限字段/)).toBeNull()
  })
})
