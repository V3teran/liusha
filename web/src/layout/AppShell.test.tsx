import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { AppShell } from './AppShell'
import { useTheme } from '@/hooks/useTheme'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/hooks/useTheme')

// 壳体测试默认已登录：mock me 探测成功，防止 AppShell 内真实 bootstrap 把
// beforeEach 设好的 authenticated 态翻成 anonymous（jsdom 无 fetch，真实调用必失败）。
vi.mock('@/api/auth', () => ({
  me: vi.fn(async () => ({ username: 'tester' })),
  getSetupStatus: vi.fn(),
  setup: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
}))

// 侧栏品牌行的版本号取自 GET /version：jsdom 无 fetch，给定一个固定版本
vi.mock('@/api/version', () => ({ getVersion: vi.fn(async () => ({ version: '0.15.0' })) }))

const mockedUseTheme = useTheme as unknown as ReturnType<typeof vi.fn>

function LoginProbe() {
  return <div>LOGIN-PAGE {useLocation().search}</div>
}

function renderShell(initialPath: string, withLoginRoute = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          {withLoginRoute && <Route path="/login" element={<LoginProbe />} />}
          <Route path="/conversations/direct" element={<AppShell />}>
            <Route index element={<div>child-active</div>} />
          </Route>
          <Route path="/findings" element={<AppShell />}>
            <Route index element={<div>child-findings</div>} />
          </Route>
          <Route path="/exploration-graph" element={<AppShell />}>
            <Route index element={<div>child-exploration-graph</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('AppShell', () => {
  beforeEach(() => {
    // 壳体测试默认已登录（守卫行为有专门用例）。
    useAuthStore.setState({ status: 'authenticated', username: 'tester' })
  })

  it('未登录（anonymous）时守卫转入 /login，不渲染壳体', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    useAuthStore.setState({ status: 'anonymous', username: '' })
    renderShell('/conversations/direct', true)

    // 记住原来要去的页面，登录后回到这里
    expect(screen.getByText('LOGIN-PAGE ?next=%2Fconversations%2Fdirect')).toBeTruthy()
    expect(screen.queryByText('对话')).toBeNull()
  })

  it('登录态探测中（checking）渲染品牌 splash 而非壳体', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    useAuthStore.setState({ status: 'checking', username: '' })
    const { container } = renderShell('/conversations/direct')

    expect(container.querySelector('img[alt="liusha"]')).toBeTruthy()
    expect(screen.queryByText('对话')).toBeNull()
  })

  it('侧栏底部渲染账号头像按钮（中性账号图标，非用户名首字母）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')

    const btn = screen.getByLabelText('账号 tester')
    expect(btn).toBeTruthy()
    expect(btn.querySelector('svg')).toBeTruthy()
    expect(btn).not.toHaveTextContent('T')
  })

  it('侧栏品牌行显示程序版本（取自 GET /version）', async () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')
    expect(await screen.findByText('v0.15.0')).toBeTruthy()
  })

  it('渲染全部导航项', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')

    expect(screen.getByText('对话')).toBeTruthy()
    expect(screen.getByText('发现')).toBeTruthy()
    expect(screen.getByText('探索图')).toBeTruthy()
    expect(screen.getByText('模型日志')).toBeTruthy()
    expect(screen.getByText('凭证库')).toBeTruthy()
    expect(screen.getByText('智能体')).toBeTruthy()
    // 左下角「设置」入口已删（/settings 路由同步移除）。
    expect(screen.queryByText('设置')).toBeNull()
  })

  it('渲染子路由内容（Outlet）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')
    expect(screen.getByText('child-active')).toBeTruthy()
  })

  it('当前路径对应的导航项高亮', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')

    const activeLink = screen.getByText('对话').closest('a')
    const inactiveLink = screen.getByText('发现').closest('a')
    expect(activeLink).toHaveAttribute('aria-current', 'page')
    expect(inactiveLink).not.toHaveAttribute('aria-current')
  })

  it('对话导航项在 /conversations 任意子路径下都高亮（startsWith 匹配）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')
    const activeLink = screen.getByText('对话').closest('a')
    expect(activeLink).toHaveAttribute('aria-current', 'page')
  })

  it('切换路径后不同导航项高亮', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/findings')

    const findingsLink = screen.getByText('发现').closest('a')
    const conversationsLink = screen.getByText('对话').closest('a')
    expect(findingsLink).toHaveAttribute('aria-current', 'page')
    expect(conversationsLink).not.toHaveAttribute('aria-current')
  })

  it('dark 主题下显示太阳图标（切换到浅色），点击调用 toggle', async () => {
    const toggle = vi.fn()
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle })
    const user = userEvent.setup()
    renderShell('/conversations/direct')

    const btn = screen.getByLabelText('切换到浅色模式')
    expect(btn).toBeTruthy()
    await user.click(btn)
    expect(toggle).toHaveBeenCalledTimes(1)
  })

  it('light 主题下显示月亮图标（切换到深色）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'light', toggle: vi.fn() })
    renderShell('/conversations/direct')

    expect(screen.getByLabelText('切换到深色模式')).toBeTruthy()
  })

  it('导航按「作业 / 观测 / 配置」分组，组名可被读屏识别', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/direct')

    const work = screen.getByRole('group', { name: '作业' })
    const observe = screen.getByRole('group', { name: '观测' })
    const config = screen.getByRole('group', { name: '配置' })
    expect(within(work).getByText('任务')).toBeTruthy()
    expect(within(observe).getByText('服务器日志')).toBeTruthy()
    expect(within(observe).getByText('审计日志')).toBeTruthy()
    expect(within(observe).getByText('模型日志')).toBeTruthy()
    expect(within(config).getByText('系统配置')).toBeTruthy()
  })

  it('当前页导航项带 aria-current="page"', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/findings')
    expect(screen.getByText('发现').closest('a')).toHaveAttribute('aria-current', 'page')
    expect(screen.getByText('对话').closest('a')).not.toHaveAttribute('aria-current')
  })
})

describe('AppShell 响应式', () => {
  beforeEach(() => {
    useAuthStore.setState({ status: 'authenticated', username: 'tester' })
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // 按视口宽度模拟 matchMedia（只解析 min-width 断点）。
  function stubViewport(width: number) {
    vi.stubGlobal(
      'matchMedia',
      vi.fn((q: string) => ({
        matches: Number(/min-width:\s*(\d+)px/.exec(q)?.[1] ?? 0) <= width,
        addEventListener: () => {},
        removeEventListener: () => {},
      })),
    )
  }

  it('平板宽度（768–1023）强制图标栏：导航项文字收起、靠 aria-label 命名', () => {
    stubViewport(900)
    renderShell('/conversations/direct')
    expect(screen.queryByText('发现')).toBeNull()
    expect(screen.getByRole('link', { name: '发现' })).toBeTruthy()
    expect(screen.queryByLabelText('收起侧边栏')).toBeNull()
  })

  it('手机宽度（< 768）侧栏收入抽屉：菜单按钮打开，Esc 关闭', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderShell('/conversations/direct')

    expect(screen.queryByRole('link', { name: '发现' })).toBeNull()
    await user.click(screen.getByRole('button', { name: '打开导航' }))
    const drawer = screen.getByRole('dialog', { name: '导航' })
    expect(within(drawer).getByText('发现')).toBeTruthy()

    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: '导航' })).toBeNull()
  })

  it('手机抽屉内点击导航项后自动关闭', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderShell('/conversations/direct')

    await user.click(screen.getByRole('button', { name: '打开导航' }))
    await user.click(within(screen.getByRole('dialog', { name: '导航' })).getByText('发现'))
    expect(screen.queryByRole('dialog', { name: '导航' })).toBeNull()
    expect(screen.getByText('child-findings')).toBeTruthy()
  })

  it('手机抽屉内账号菜单可用：修改密码弹窗能从模态抽屉里打开', async () => {
    // 模态抽屉打开期间 body 为 pointer-events:none；菜单若不在 Radix 分层栈里就点不到。
    stubViewport(390)
    const user = userEvent.setup()
    renderShell('/conversations/direct')

    await user.click(screen.getByRole('button', { name: '打开导航' }))
    await user.click(within(screen.getByRole('dialog', { name: '导航' })).getByLabelText('账号 tester'))
    await user.click(await screen.findByRole('menuitem', { name: '修改密码' }))
    expect(await screen.findByRole('dialog', { name: '修改密码' })).toBeTruthy()
  })
})
