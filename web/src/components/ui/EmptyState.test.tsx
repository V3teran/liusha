import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EmptyState, emptyTitle } from './EmptyState'

describe('emptyTitle', () => {
  it('标题句式由成因决定', () => {
    expect(emptyTitle('empty', '任务')).toBe('暂无任务')
    expect(emptyTitle('no-match', '任务')).toBe('没有匹配的任务')
    expect(emptyTitle('unselected', '任务')).toBe('未选择任务')
  })

  it('西文名词前留一个空格', () => {
    expect(emptyTitle('empty', 'LLM 调用记录')).toBe('暂无 LLM 调用记录')
    expect(emptyTitle('no-match', 'provider')).toBe('没有匹配的 provider')
  })
})

describe('EmptyState', () => {
  it('无数据：标题 + 调用方给的说明', () => {
    render(<EmptyState kind="empty" noun="漏洞" hint="任务运行中经复现验证的漏洞会出现在这里。" />)
    expect(screen.getByText('暂无漏洞')).toBeInTheDocument()
    expect(screen.getByText('任务运行中经复现验证的漏洞会出现在这里。')).toBeInTheDocument()
  })

  it('无匹配：带筛选栏的页面提示调整筛选，只有搜索框的提示换关键词', () => {
    const { rerender } = render(<EmptyState kind="no-match" noun="漏洞" />)
    expect(screen.getByText('没有匹配的漏洞')).toBeInTheDocument()
    expect(screen.getByText('换个关键词或调整筛选条件。')).toBeInTheDocument()
    rerender(<EmptyState kind="no-match" scope="search" noun="凭证" />)
    expect(screen.getByText('换个关键词试试。')).toBeInTheDocument()
  })

  it('图标仅作装饰，不进无障碍树', () => {
    const { container } = render(<EmptyState kind="unselected" noun="文件" hint="选择一个文件查看内容。" />)
    expect(container.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
  })
})
