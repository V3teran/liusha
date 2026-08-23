import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { ConfigListShell, ConfigRow } from './ConfigListShell'

// 造 n 张卡片作为 children，用序号当 code/name 便于断言当前页可见项。
function cards(n: number) {
  return Array.from({ length: n }, (_, i) => (
    <ConfigRow key={i} code={`c${i}`} name={`item-${i}`} onClick={() => {}} />
  ))
}

function renderShell(n: number) {
  return render(
    <ConfigListShell
      title="场景"
      subtitle="sub"
      loading={false}
      error=""
      empty={n === 0}
      emptyHint="空"
      onNew={vi.fn()}
    >
      {cards(n)}
    </ConfigListShell>,
  )
}

describe('ConfigListShell 分页', () => {
  it('不足一页（≤12 项）时仍显示翻页条，但下一页置灰', () => {
    renderShell(12)
    const next = screen.getByText('下一页').closest('button')!
    expect(next).toBeDisabled()
    expect(screen.getByText('共 12 项')).toBeTruthy()
    expect(screen.getByText('item-11')).toBeTruthy()
  })

  it('超过一页时只显示当前页的 12 项，翻页条可见', () => {
    renderShell(13)
    expect(screen.getByText('item-0')).toBeTruthy()
    expect(screen.getByText('item-11')).toBeTruthy()
    expect(screen.queryByText('item-12')).toBeNull() // 第 13 项落到第 2 页
    expect(screen.getByText('第 1 / 2 页')).toBeTruthy()
    expect(screen.getByText('共 13 项')).toBeTruthy()
  })

  it('点「下一页」翻到第 2 页，边界按钮正确禁用', () => {
    renderShell(13)
    const prev = screen.getByText('上一页').closest('button')!
    const next = screen.getByText('下一页').closest('button')!
    expect(prev).toBeDisabled() // 第 1 页无上一页

    fireEvent.click(next)

    expect(screen.getByText('第 2 / 2 页')).toBeTruthy()
    expect(screen.getByText('item-12')).toBeTruthy()
    expect(screen.queryByText('item-0')).toBeNull()
    expect(next).toBeDisabled() // 末页无下一页
    expect(prev).not.toBeDisabled()
  })

  it('空态显示提示、不渲染网格与翻页条', () => {
    renderShell(0)
    expect(screen.getByText('空')).toBeTruthy()
    expect(screen.queryByText('下一页')).toBeNull()
  })
})
