import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CompactionCard } from './CompactionCard'

describe('CompactionCard', () => {
  it('默认收起，只显示 label', () => {
    render(<CompactionCard label="压缩了 12 条历史消息" summary="蒸馏摘要正文" />)
    expect(screen.getByText(/压缩了 12 条历史消息/)).toBeTruthy()
    expect(screen.queryByText('蒸馏摘要正文')).toBeFalsy()
    expect(screen.getByText('看摘要')).toBeTruthy()
  })

  it('点击展开显示摘要正文', async () => {
    render(<CompactionCard label="压缩了 12 条历史消息" summary="蒸馏摘要正文" />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.getByText('蒸馏摘要正文')).toBeTruthy()
    expect(screen.getByText('收起')).toBeTruthy()
  })

  it('无 label 时不显示分隔符', () => {
    render(<CompactionCard label="" summary="摘要" />)
    expect(screen.getByText('上下文已压缩')).toBeTruthy()
  })

  it('再次点击收起', async () => {
    render(<CompactionCard label="x" summary="摘要正文" />)
    const btn = screen.getByRole('button')
    await userEvent.click(btn)
    expect(screen.getByText('摘要正文')).toBeTruthy()
    await userEvent.click(btn)
    expect(screen.queryByText('摘要正文')).toBeFalsy()
  })
})
