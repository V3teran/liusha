import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ProviderFormBody, providerInvalid, blankProvider } from './providerForm'
import type { ProviderConfig } from '@/api/types'

// 数字字段契约：用户可清空（不回写 0）、可输入任意前导 0（显示忠实于键入），
// 变更实时回写 draft 数值。此前 onChange 直接 Number(e.target.value)：
// Number("")=0 让「清空」变成「填 0」，受控回写让字段卡着删不掉的 0。
const draft = (over: Partial<ProviderConfig> = {}): ProviderConfig => ({
  ...blankProvider(),
  base_url: 'https://x.example',
  default_model: 'm1',
  api_key: 'k',
  context_window: 65536,
  max_tokens: 4096,
  ...over,
})

function renderForm(over: Partial<ProviderConfig> = {}, patchSpy = vi.fn()) {
  render(
    <ProviderFormBody
      draft={draft(over)}
      patch={patchSpy}
      isNew
      onRestoreEnvKey={undefined}
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
      <ProviderFormBody draft={draft()} patch={vi.fn()} isNew onRestoreEnvKey={undefined} />,
    )
    const input = screen.getByLabelText(/上下文窗口/)
    await user.clear(input)
    await user.type(input, '123')
    rerender(
      <ProviderFormBody draft={draft({ context_window: 999999 })} patch={vi.fn()} isNew onRestoreEnvKey={undefined} />,
    )
    expect((input as HTMLInputElement).value).toBe('999999')
  })
})

describe('providerInvalid', () => {
  it('context_window 为空串/0 时表单无效（保存禁用），>0 时有效', () => {
    expect(providerInvalid(draft({ context_window: '' as unknown as number }), true)).toBe(true)
    expect(providerInvalid(draft({ context_window: 0 }), true)).toBe(true)
    expect(providerInvalid(draft({ context_window: 1 }), true)).toBe(false)
  })
})
