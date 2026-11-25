import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'

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

// 外壳自身渲染出错
vi.mock('@/layout/AppShell', () => ({
  AppShell: () => {
    throw new Error('外壳炸了')
  },
}))

import { router } from './router'

vi.spyOn(console, 'error').mockImplementation(() => {})

describe('router 的根级错误页', () => {
  it('外壳自身出错：整页由中文错误页接住，不落路由库的英文默认页', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('页面出错了')).toBeInTheDocument()
    expect(within(alert).getByText('外壳炸了')).toBeInTheDocument()
    expect(within(alert).getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '主导航' })).toBeNull()
    expect(screen.queryByText(/Unexpected Application Error/)).toBeNull()
  })
})
