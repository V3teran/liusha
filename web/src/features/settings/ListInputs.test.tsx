import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ListTextarea, CodesInput } from './ListInputs'

function ListHarness({ initial }: { initial: string[] }) {
  const [v, setV] = useState(initial)
  return (
    <>
      <ListTextarea aria-label="名单" value={v} onChange={setV} />
      <output data-testid="list">{JSON.stringify(v)}</output>
      <button onClick={() => setV(['reset.example'])}>外部重置</button>
    </>
  )
}

function CodesHarness({ initial }: { initial: number[] }) {
  const [v, setV] = useState(initial)
  return (
    <>
      <CodesInput aria-label="状态码" value={v} onChange={setV} />
      <output data-testid="codes">{JSON.stringify(v)}</output>
    </>
  )
}

describe('ListTextarea', () => {
  // 逐键输入：末尾回车再输入下一项，应得到两项，而不是被即时归一化吞掉换行拼成一项。
  it('末尾换行后追加新项', async () => {
    render(<ListHarness initial={['a.example']} />)
    const box = screen.getByLabelText('名单')
    await userEvent.click(box)
    await userEvent.keyboard('{End}{Enter}b.example')
    expect(screen.getByTestId('list').textContent).toBe(JSON.stringify(['a.example', 'b.example']))
    expect((box as HTMLTextAreaElement).value).toBe('a.example\nb.example')
  })

  it('外部值变化（加载 / 恢复默认 / 放弃）时重排文本', async () => {
    render(<ListHarness initial={['a.example']} />)
    await userEvent.click(screen.getByText('外部重置'))
    expect((screen.getByLabelText('名单') as HTMLTextAreaElement).value).toBe('reset.example')
  })
})

describe('CodesInput', () => {
  it('逗号加空格分隔逐键追加状态码', async () => {
    render(<CodesHarness initial={[304]} />)
    const input = screen.getByLabelText('状态码')
    await userEvent.click(input)
    await userEvent.keyboard('{End}, 403')
    expect(screen.getByTestId('codes').textContent).toBe(JSON.stringify([304, 403]))
    expect((input as HTMLInputElement).value).toBe('304, 403')
  })
})
