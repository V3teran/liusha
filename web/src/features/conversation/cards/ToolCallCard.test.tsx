import { useState, type ComponentProps } from 'react'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToolCallCard } from './ToolCallCard'

type Props = Omit<ComponentProps<typeof ToolCallCard>, 'open' | 'onToggle'>

// 展开状态由时间线持有；单独测这张卡时用一个最小的持态外壳。
function Show(props: Props) {
  const [open, setOpen] = useState(false)
  return <ToolCallCard {...props} open={open} onToggle={() => setOpen((o) => !o)} />
}

describe('ToolCallCard', () => {
  it('渲染工具名，默认折叠不显示参数', () => {
    render(<Show tool="command_run" args='{"cmd":"ls"}' />)
    expect(screen.getByText('command_run')).toBeTruthy()
    expect(screen.queryByText(/"cmd"/)).toBeNull()
  })

  it('点击展开显示美化后的参数 JSON', async () => {
    const user = userEvent.setup()
    render(<Show tool="command_run" args='{"cmd":"ls"}' />)
    await user.click(screen.getByText('command_run'))
    expect(screen.getByText(/"cmd": "ls"/)).toBeTruthy()
  })

  it('展开状态可由外部置位（时间线持有，卡换位置重挂后照旧展开）', () => {
    render(<ToolCallCard tool="command_run" args='{"cmd":"ls"}' open onToggle={() => {}} />)
    expect(screen.getByText(/"cmd": "ls"/)).toBeTruthy()
  })

  it('空对象参数视为无参数，展开后不渲染 pre', async () => {
    const user = userEvent.setup()
    const { container } = render(<Show tool="noop" args="{}" />)
    await user.click(screen.getByText('noop'))
    expect(container.querySelector('pre')).toBeNull()
  })

  it('非法 JSON 参数原样展示', async () => {
    const user = userEvent.setup()
    render(<Show tool="raw" args="not-json" />)
    await user.click(screen.getByText('raw'))
    expect(screen.getByText('not-json')).toBeTruthy()
  })

  it('身份色落在左侧色条上，卡内不重复身份标签（归属由它所在的轮卡表明）', () => {
    const { container } = render(<Show tool="x" args="" accent="#8b5cf6" />)
    expect(container.querySelector('[data-card="tool-call"] button')?.getAttribute('style')).toContain('rgb(139, 92, 246)')
    expect(screen.queryByText('执行')).toBeNull()
  })

  it('空参数字符串不渲染展开内容', async () => {
    const user = userEvent.setup()
    const { container } = render(<Show tool="x" args="" />)
    await user.click(screen.getByText('x'))
    expect(container.querySelector('pre')).toBeNull()
  })

  it('执行中态：spinner 替换状态点 + 「执行中…」提示', () => {
    const { container } = render(<Show tool="browser_drive" args="" accent="#059669" running />)
    expect(screen.getByText('执行中…')).toBeTruthy()
    expect(container.querySelector('.animate-spin')).toBeTruthy()
    expect(container.querySelector('[data-card="tool-call"]')?.getAttribute('data-running')).toBe('true')
  })

  it('非执行中态无 spinner 与提示', () => {
    const { container } = render(<Show tool="x" args="" />)
    expect(screen.queryByText('执行中…')).toBeNull()
    expect(container.querySelector('.animate-spin')).toBeNull()
  })
})
