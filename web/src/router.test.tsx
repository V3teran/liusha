import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'

// 路由在求值时读当前地址：先落到要测的页面，再加载路由表
vi.hoisted(() => {
  window.history.replaceState({}, '', '/tasks')
})

vi.mock('@/hooks/useTheme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))
vi.mock('@/api/auth', () => ({
  me: vi.fn(async () => ({ username: 'tester' })),
  getSetupStatus: vi.fn(),
  setup: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
}))
vi.mock('@/api/version', () => ({ getVersion: vi.fn(async () => ({ version: '0.15.0' })) }))

// 任务页的组件在渲染时抛错。代码块取不到（程序升级后，开着旧版控制台的浏览器还在请求旧文件）
// 走的也是这条路：懒加载失败后同样在渲染期抛给路由的错误边界；那一条由浏览器实测覆盖。
vi.mock('@/pages/TasksPage', () => ({
  TasksPage: () => {
    throw new TypeError('Failed to fetch dynamically imported module: /assets/TasksPage-abc.js')
  },
}))

import { router } from './router'

// 失败原因由路由库的错误边界打到控制台：拦下来，不让输出刷屏
vi.spyOn(console, 'error').mockImplementation(() => {})

function renderApp() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe('router 的页面级错误页', () => {
  it('页面渲染出错：外壳内显示中文错误页与「重新加载」，侧栏仍在', async () => {
    renderApp()

    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('页面出错了')).toBeInTheDocument()
    expect(within(alert).getByText(/Failed to fetch dynamically imported module/)).toBeInTheDocument()
    expect(within(alert).getByRole('button', { name: '重新加载' })).toBeInTheDocument()

    // 出错的是页面，不是外壳：侧栏留着，用户可以离开这一页
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument()
    // 不再落到路由库自带的英文默认页
    expect(screen.queryByText(/Unexpected Application Error/)).toBeNull()
  })
})
