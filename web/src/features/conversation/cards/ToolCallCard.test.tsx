import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToolCallCard } from './ToolCallCard'

describe('ToolCallCard', () => {
  it('渲染工具名，默认折叠不显示参数', () => {
    render(<ToolCallCard tool="run_command" args='{"cmd":"ls"}' />)
    expect(screen.getByText('run_command')).toBeTruthy()
    expect(screen.queryByText(/"cmd"/)).toBeNull()
  })

  it('点击展开显示美化后的参数 JSON', async () => {
    const user = userEvent.setup()
    render(<ToolCallCard tool="run_command" args='{"cmd":"ls"}' />)
    await user.click(screen.getByText('run_command'))
    expect(screen.getByText(/"cmd": "ls"/)).toBeTruthy()
  })

  it('空对象参数视为无参数，展开后不渲染 pre', async () => {
    const user = userEvent.setup()
    const { container } = render(<ToolCallCard tool="noop" args="{}" />)
    await user.click(screen.getByText('noop'))
    expect(container.querySelector('pre')).toBeNull()
  })

  it('非法 JSON 参数原样展示', async () => {
    const user = userEvent.setup()
    render(<ToolCallCard tool="raw" args="not-json" />)
    await user.click(screen.getByText('raw'))
    expect(screen.getByText('not-json')).toBeTruthy()
  })

  it('有 agentName 时显示中文标签', () => {
    render(<ToolCallCard tool="x" args="" agentName="reconnaissance" />)
    expect(screen.getByText('侦察')).toBeTruthy()
  })

  it('无 agentName 时不显示标签', () => {
    render(<ToolCallCard tool="x" args="" />)
    // 只应有工具名一个文字节点，标签 chip 不存在
    expect(screen.queryByText('侦察')).toBeNull()
  })

  it('空参数字符串不渲染展开内容', async () => {
    const user = userEvent.setup()
    const { container } = render(<ToolCallCard tool="x" args="" />)
    await user.click(screen.getByText('x'))
    expect(container.querySelector('pre')).toBeNull()
  })
})
