import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToolResultCard } from './ToolResultCard'

describe('ToolResultCard', () => {
  it('成功结果默认折叠，显示预览文本', () => {
    render(<ToolResultCard tool="run_command" result="line1 line2" durationMs={120} err="" />)
    expect(screen.getByText('run_command')).toBeTruthy()
    expect(screen.getByText('120ms')).toBeTruthy()
    expect(screen.getByText('line1 line2')).toBeTruthy()
  })

  it('错误结果默认展开（不显示预览，直接显示 pre）', () => {
    const { container } = render(<ToolResultCard tool="x" result="" durationMs={5} err="boom" />)
    expect(container.querySelector('[data-error="true"]')).toBeTruthy()
    expect(container.querySelector('pre')?.textContent).toBe('boom')
  })

  it('点击折叠已展开的错误结果', async () => {
    const user = userEvent.setup()
    const { container } = render(<ToolResultCard tool="x" result="" durationMs={5} err="boom" />)
    const btn = screen.getByText('x').closest('button')!
    await user.click(btn)
    expect(container.querySelector('pre')).toBeNull()
  })

  it('点击展开成功结果显示美化 JSON', async () => {
    const user = userEvent.setup()
    render(<ToolResultCard tool="x" result='{"ok":true}' durationMs={10} err="" />)
    await user.click(screen.getByText('x'))
    expect(screen.getByText(/"ok": true/)).toBeTruthy()
  })

  it('预览超过 64 字符截断加省略号', () => {
    const long = 'x'.repeat(100)
    render(<ToolResultCard tool="x" result={long} durationMs={1} err="" />)
    expect(screen.getByText(/…$/)).toBeTruthy()
  })

  it('预览文本折叠多余空白', () => {
    render(<ToolResultCard tool="x" result={'a\n\n  b   c'} durationMs={1} err="" />)
    expect(screen.getByText('a b c')).toBeTruthy()
  })

  it('有 agentName 时显示中文标签', () => {
    render(<ToolResultCard tool="x" result="ok" durationMs={1} err="" agentName="planner" />)
    expect(screen.getByText('编排')).toBeTruthy()
  })

  it('无结果无错误时预览为空字符串（不崩）', () => {
    const { container } = render(<ToolResultCard tool="x" result="" durationMs={0} err="" />)
    expect(container.querySelector('[data-card="tool-result"]')).toBeTruthy()
  })
})
