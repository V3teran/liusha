import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { Combobox } from './Combobox'

const OPTIONS = ['glm-4.5', 'glm-4.5-air', 'glm-4.6', 'glm-5.3-flash']

function Harness({ initial = '' }: { initial?: string }) {
  const [v, setV] = useState(initial)
  return <Combobox value={v} options={OPTIONS} onChange={setV} toggleLabel="展开模型列表" />
}

const input = () => screen.getByRole('combobox')
const shown = () => screen.queryAllByRole('option').map((o) => o.textContent)

describe('Combobox', () => {
  it('已有值时点展开按钮仍列出全部选项，当前值标为已选', async () => {
    render(<Harness initial="glm-4.5" />)
    await userEvent.click(screen.getByRole('button', { name: '展开模型列表' }))
    expect(shown()).toEqual(OPTIONS)
    expect(screen.getByRole('option', { name: 'glm-4.5' }).getAttribute('aria-selected')).toBe('true')
    expect(input().getAttribute('aria-expanded')).toBe('true')
  })

  it('点选项即改选并收起', async () => {
    render(<Harness initial="glm-4.5" />)
    await userEvent.click(screen.getByRole('button', { name: '展开模型列表' }))
    await userEvent.click(screen.getByRole('option', { name: 'glm-5.3-flash' }))
    expect((input() as HTMLInputElement).value).toBe('glm-5.3-flash')
    expect(input().getAttribute('aria-expanded')).toBe('false')
  })

  it('输入时按输入内容筛选，可自由填写列表外的值', async () => {
    render(<Harness />)
    await userEvent.type(input(), '4.5')
    expect(shown()).toEqual(['glm-4.5', 'glm-4.5-air'])
    await userEvent.clear(input())
    await userEvent.type(input(), 'custom-model')
    expect(shown()).toEqual([])
    expect((input() as HTMLInputElement).value).toBe('custom-model')
  })

  it('键盘：↓ 展开全部并移动高亮，Enter 选中，Esc 收起', async () => {
    render(<Harness initial="glm-4.5" />)
    await userEvent.click(input())
    await userEvent.keyboard('{ArrowDown}')
    expect(shown()).toEqual(OPTIONS)
    await userEvent.keyboard('{ArrowDown}{Enter}')
    expect((input() as HTMLInputElement).value).toBe('glm-4.5-air')
    await userEvent.keyboard('{ArrowDown}')
    expect(input().getAttribute('aria-expanded')).toBe('true')
    await userEvent.keyboard('{Escape}')
    expect(input().getAttribute('aria-expanded')).toBe('false')
  })

  it('没有选项时只是普通输入框', () => {
    render(<Combobox value="" options={[]} onChange={() => {}} />)
    expect(screen.queryByRole('button')).toBeNull()
  })
})
