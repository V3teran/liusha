import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ToolPicker, type PickerCandidate } from './ToolPicker'

const catalog: PickerCandidate[] = [
  { name: 'nmap', category: 'recon', category_name: '侦察', description: '端口扫描', installed: true },
  { name: 'sqlmap', category: 'injection', category_name: '注入', description: 'SQL 注入', installed: false },
  { name: 'ffuf', category: 'discovery', category_name: '发现', description: '目录爆破', installed: false },
]

describe('ToolPicker', () => {
  it('沙盒未安装的工具标「未安装」，并统计已允许但未安装的数量', () => {
    render(<ToolPicker catalog={catalog} draft={{ all: false, checked: ['nmap', 'sqlmap'] }} onToggle={vi.fn()} emptyHint="" />)
    // 已允许 sqlmap 未安装；ffuf 未安装但未允许，不计入。
    expect(screen.getByText(/已允许 · 未安装 1 个/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'sqlmap · 未安装' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('button', { name: 'ffuf · 未安装' }).getAttribute('aria-pressed')).toBe('false')
    expect(screen.getByRole('button', { name: 'nmap' })).toBeTruthy()
  })

  it('全部已安装或状态未知时不提示', () => {
    const unknown = catalog.map((t) => ({ ...t, installed: null }))
    render(<ToolPicker catalog={unknown} draft={{ all: true, checked: ['nmap', 'sqlmap', 'ffuf'] }} onToggle={vi.fn()} emptyHint="" />)
    expect(screen.queryByText(/已允许 · 未安装/)).toBeNull()
    expect(screen.queryByText(/未安装/)).toBeNull()
  })
})
