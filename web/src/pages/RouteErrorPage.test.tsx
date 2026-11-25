import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { RouteErrorPage } from './RouteErrorPage'

// 失败原因由路由库的错误边界打到控制台：拦下来，不让输出刷屏
vi.spyOn(console, 'error').mockImplementation(() => {})

// 与真实路由同一机制：页面组件在渲染时抛错，由路由的 errorElement 接住（错误边界原样拿到抛出的值）。
// 不用 loader 抛错：路由库会把它重新包装，拿不到原值。
function Throwing({ error }: { error: unknown }): never {
  throw error
}

function renderOnError(error: unknown) {
  const reload = vi.fn()
  const router = createMemoryRouter(
    [{ path: '/', element: <Throwing error={error} />, errorElement: <RouteErrorPage reload={reload} /> }],
    { initialEntries: ['/'] },
  )
  render(<RouterProvider router={router} />)
  return reload
}

describe('RouteErrorPage', () => {
  it('显示标题与失败原因，点「重新加载」重新加载页面', async () => {
    const error = new TypeError('Failed to fetch dynamically imported module: /assets/TasksPage-abc.js')
    const reload = renderOnError(error)

    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('页面出错了')).toBeInTheDocument()
    expect(within(alert).getByText(error.message)).toBeInTheDocument()

    await userEvent.click(within(alert).getByRole('button', { name: '重新加载' }))
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('抛出的不是 Error 对象时，原因显示它的文本', async () => {
    renderOnError('外壳炸了')

    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('外壳炸了')).toBeInTheDocument()
  })

  it('没有可显示的原因时不留空段落', async () => {
    renderOnError(new Error(''))

    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('页面出错了')).toBeInTheDocument()
    expect(alert.querySelectorAll('p')).toHaveLength(1)
  })
})
