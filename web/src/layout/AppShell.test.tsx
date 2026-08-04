import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AppShell } from './AppShell'
import { useTheme } from '@/hooks/useTheme'

vi.mock('@/hooks/useTheme')

const mockedUseTheme = useTheme as unknown as ReturnType<typeof vi.fn>

function renderShell(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/conversations/manual" element={<AppShell />}>
          <Route index element={<div>child-active</div>} />
        </Route>
        <Route path="/findings" element={<AppShell />}>
          <Route index element={<div>child-findings</div>} />
        </Route>
        <Route path="/attack-graph" element={<AppShell />}>
          <Route index element={<div>child-attack-graph</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('AppShell', () => {
  it('渲染全部导航项', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/manual')

    expect(screen.getByText('对话')).toBeTruthy()
    expect(screen.getByText('漏洞管理')).toBeTruthy()
    expect(screen.getByText('攻击面')).toBeTruthy()
    expect(screen.getByText('执行图')).toBeTruthy()
    expect(screen.getByText('LLM 审计')).toBeTruthy()
    expect(screen.getByText('凭证库')).toBeTruthy()
    expect(screen.getByText('场景')).toBeTruthy()
    expect(screen.getByText('剧本')).toBeTruthy()
    expect(screen.getByText('智能体')).toBeTruthy()
    expect(screen.getByText('设置')).toBeTruthy()
  })

  it('渲染子路由内容（Outlet）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/manual')
    expect(screen.getByText('child-active')).toBeTruthy()
  })

  it('当前路径对应的导航项高亮', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/manual')

    const activeLink = screen.getByText('对话').closest('a')
    const inactiveLink = screen.getByText('漏洞管理').closest('a')
    expect(activeLink?.className).toContain('bg-accent-soft')
    expect(inactiveLink?.className).not.toContain('bg-accent-soft')
  })

  it('对话导航项在 /conversations 任意子路径下都高亮（startsWith 匹配）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/conversations/manual')
    const activeLink = screen.getByText('对话').closest('a')
    expect(activeLink?.className).toContain('bg-accent-soft')
  })

  it('切换路径后不同导航项高亮', () => {
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle: vi.fn() })
    renderShell('/findings')

    const findingsLink = screen.getByText('漏洞管理').closest('a')
    const conversationsLink = screen.getByText('对话').closest('a')
    expect(findingsLink?.className).toContain('bg-accent-soft')
    expect(conversationsLink?.className).not.toContain('bg-accent-soft')
  })

  it('dark 主题下显示太阳图标（切换到浅色），点击调用 toggle', async () => {
    const toggle = vi.fn()
    mockedUseTheme.mockReturnValue({ theme: 'dark', toggle })
    const user = userEvent.setup()
    renderShell('/conversations/manual')

    const btn = screen.getByLabelText('切换到浅色')
    expect(btn).toBeTruthy()
    await user.click(btn)
    expect(toggle).toHaveBeenCalledTimes(1)
  })

  it('light 主题下显示月亮图标（切换到深色）', () => {
    mockedUseTheme.mockReturnValue({ theme: 'light', toggle: vi.fn() })
    renderShell('/conversations/manual')

    expect(screen.getByLabelText('切换到深色')).toBeTruthy()
  })
})
