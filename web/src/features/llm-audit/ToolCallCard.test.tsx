import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ToolCallCard } from './ToolCallCard'
import type { ToolCallView } from '@/lib/toolCalls'

function makeCall(overrides: Partial<ToolCallView> = {}): ToolCallView {
  return { id: 'call-1', name: 'run_command', args: '', ...overrides }
}

describe('ToolCallCard', () => {
  it('渲染工具名', () => {
    render(<ToolCallCard call={makeCall({ name: 'http_request' })} />)
    expect(screen.getByText('http_request')).toBeTruthy()
  })

  it('有参数时渲染美化后的参数', () => {
    const args = JSON.stringify({ url: 'http://x' }, null, 2)
    const { container } = render(<ToolCallCard call={makeCall({ args })} />)
    expect(container.querySelector('pre')?.textContent).toBe(args)
  })

  it('无参数时显示「无参数」占位', () => {
    render(<ToolCallCard call={makeCall({ args: '' })} />)
    expect(screen.getByText('无参数')).toBeTruthy()
  })
})
