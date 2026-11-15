import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import { EntityPicker, type PickerResult } from './EntityPicker'

const ALL: PickerResult = {
  options: [
    { value: 't-1', label: '测试商城 shop.local', detail: '11-14 08:30 · 3 个' },
    { value: 't-2', label: '测试博客 blog.local', detail: '11-13 20:30 · 1 个' },
  ],
  hasMore: true,
}

function Harness({ fetchOptions, initial = '' }: { fetchOptions: (q: string) => Promise<PickerResult>; initial?: string }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <EntityPicker
        value={value}
        valueLabel={value ? `选中 ${value}` : undefined}
        onChange={setValue}
        fetchOptions={fetchOptions}
        queryKey={['picker-test']}
        emptyLabel="全部任务"
        searchPlaceholder="搜索任务目标"
        ariaLabel="按任务筛选"
      />
      <output data-testid="value">{value}</output>
    </>
  )
}

function renderPicker(fetchOptions: (q: string) => Promise<PickerResult>, initial = '') {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Harness fetchOptions={fetchOptions} initial={initial} />
    </QueryClientProvider>,
  )
}

describe('EntityPicker', () => {
  it('展开才查询；候选带次要信息；超出上限时提示缩小范围', async () => {
    const fetchOptions = vi.fn(async () => ALL)
    const user = userEvent.setup()
    renderPicker(fetchOptions)
    expect(fetchOptions).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '按任务筛选：全部任务' }))
    expect(await screen.findByRole('option', { name: /测试商城 shop.local/ })).toBeTruthy()
    expect(screen.getByText('11-14 08:30 · 3 个')).toBeTruthy()
    expect(screen.getByText('仅列出最近的部分，输入关键词缩小范围')).toBeTruthy()
  })

  it('输入关键词由服务端检索；键盘选中', async () => {
    const fetchOptions = vi.fn(async (q: string) => (q ? { options: [ALL.options[1]], hasMore: false } : ALL))
    const user = userEvent.setup()
    renderPicker(fetchOptions)
    await user.click(screen.getByRole('button', { name: /按任务筛选/ }))
    await user.type(screen.getByRole('combobox', { name: '搜索任务目标' }), '博客')
    await waitFor(() => expect(fetchOptions).toHaveBeenCalledWith('博客'))
    await screen.findByRole('option', { name: /测试博客/ })
    await waitFor(() => expect(screen.queryByRole('option', { name: /测试商城/ })).toBeNull())
    await user.keyboard('{Enter}')
    expect(screen.getByTestId('value').textContent).toBe('t-2')
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('已选中时显示选中项的名字，首项可清除选择', async () => {
    const user = userEvent.setup()
    renderPicker(async () => ALL, 't-9')
    await user.click(screen.getByRole('button', { name: '按任务筛选：选中 t-9' }))
    await user.click(await screen.findByRole('option', { name: '全部任务' }))
    expect(screen.getByTestId('value').textContent).toBe('')
  })

  it('没有匹配时如实提示', async () => {
    const user = userEvent.setup()
    renderPicker(async (q) => (q ? { options: [], hasMore: false } : ALL))
    await user.click(screen.getByRole('button', { name: /按任务筛选/ }))
    await user.type(screen.getByRole('combobox'), '不存在')
    expect(await screen.findByText('没有匹配项')).toBeTruthy()
  })
})
