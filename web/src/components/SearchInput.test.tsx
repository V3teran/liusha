import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SearchInput } from './SearchInput'
import { FilterBar } from './FilterBar'

// 输入框里只留左边那一个放大镜（装饰）：框内再放一个放大镜按钮，两个同样的图标并排，
// 分不清哪个是装饰哪个是操作。
const magnifierButtons = (container: HTMLElement) =>
  [...container.querySelectorAll('button')].filter((b) => b.querySelector('.lucide-search')).length

describe('SearchInput', () => {
  it('纯本地过滤的搜索框（不传 onRefresh）没有刷新钮', () => {
    const { container } = render(<SearchInput value="x" onChange={() => {}} />)
    expect(screen.queryByRole('button', { name: '刷新' })).toBeNull()
    expect(magnifierButtons(container)).toBe(0)
  })

  // 「想看最新数据时不知道点哪里」：刷新钮在输入框外右侧，按当前的词重取一遍。
  it('刷新钮在输入框外，点了重取；图标是刷新箭头而不是又一个放大镜', async () => {
    const user = userEvent.setup()
    const onRefresh = vi.fn()
    const { container } = render(
      <FilterBar label="筛选" canReset={false} onReset={() => {}}>
        <SearchInput value="xss" onChange={() => {}} onRefresh={onRefresh} />
      </FilterBar>,
    )
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh.querySelector('.lucide-refresh-cw')).toBeTruthy()
    // 刷新钮是输入框的兄弟，不在它里面
    expect(refresh.closest('input, .relative')).toBeNull()
    expect(magnifierButtons(container)).toBe(0)
    await user.click(refresh)
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('回车等同提交，且不提交外层表单', () => {
    const onSubmit = vi.fn()
    const onSubmitForm = vi.fn()
    render(
      <form onSubmit={onSubmitForm}>
        <SearchInput value="" onChange={() => {}} onSubmit={onSubmit} onRefresh={() => {}} />
      </form>,
    )
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmitForm).not.toHaveBeenCalled()
  })

  it('正在重取时刷新钮禁用、图标转动', () => {
    render(<SearchInput value="" onChange={() => {}} onRefresh={() => {}} refreshing />)
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    expect(refresh.querySelector('.animate-spin')).toBeTruthy()
  })

  it('独立使用且有内容时框内有清空钮；位于筛选栏内时不放（由栏的「清空筛选」负责）', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { unmount } = render(<SearchInput value="xss" onChange={onChange} onRefresh={() => {}} />)
    await user.click(screen.getByRole('button', { name: '清空搜索' }))
    expect(onChange).toHaveBeenCalledWith('')
    unmount()

    render(
      <FilterBar label="筛选" canReset onReset={() => {}}>
        <SearchInput value="xss" onChange={() => {}} onRefresh={() => {}} />
      </FilterBar>,
    )
    expect(screen.queryByRole('button', { name: '清空搜索' })).toBeNull()
  })

  it('传了 trailing 时只渲染它，不放清空钮与刷新钮', () => {
    render(<SearchInput value="xss" onChange={() => {}} onRefresh={() => {}} trailing={<button type="button">附件</button>} />)
    expect(screen.getByRole('button', { name: '附件' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '清空搜索' })).toBeNull()
    expect(screen.queryByRole('button', { name: '刷新' })).toBeNull()
  })
})
