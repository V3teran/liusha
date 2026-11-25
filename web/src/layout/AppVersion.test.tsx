import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { formatVersion } from './AppVersion'
import { Sidebar } from './Sidebar'

const getVersion = vi.hoisted(() => vi.fn())
vi.mock('@/api/version', () => ({ getVersion }))
vi.mock('@/hooks/useTheme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))

function renderSidebar(rail: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Sidebar rail={rail} onToggleRail={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('formatVersion', () => {
  it('以数字开头的版本加 v（与发布标签同形），dev 之类的原样', () => {
    expect(formatVersion('0.15.0')).toBe('v0.15.0')
    expect(formatVersion('0.14.2-3-g1a2b3cd')).toBe('v0.14.2-3-g1a2b3cd')
    expect(formatVersion('dev')).toBe('dev')
  })
})

// 版本是程序告诉前端的（GET /version），前端不自带：取不到就不显示，不留占位。
describe('侧栏的程序版本', () => {
  it('展开时显示在品牌名旁边，读屏读作「版本」', async () => {
    getVersion.mockResolvedValue({ version: '0.15.0' })
    renderSidebar(false)
    const shown = await screen.findByText('v0.15.0')
    // 可见文字只有 v0.15.0（语音控制照屏幕念选得中）；「版本」两个字只给读屏
    expect(shown.textContent).toBe('版本 v0.15.0')
    expect(shown.querySelector('.sr-only')?.textContent).toBe('版本 ')
    expect(shown).toHaveAttribute('title', 'liusha 0.15.0')
    expect(screen.getByRole('img', { name: 'liusha' })).not.toHaveAttribute('title')
  })

  it('图标栏里放不下版本号：挂在徽记的悬停提示上', async () => {
    getVersion.mockResolvedValue({ version: '0.15.0' })
    renderSidebar(true)
    await waitFor(() => expect(screen.getByRole('img', { name: 'liusha' })).toHaveAttribute('title', 'liusha v0.15.0'))
    expect(screen.queryByText('v0.15.0')).toBeNull()
  })

  it('取不到版本时什么都不显示', async () => {
    getVersion.mockRejectedValue(new Error('network'))
    renderSidebar(false)
    await waitFor(() => expect(getVersion).toHaveBeenCalled())
    expect(screen.queryByText(/^v\d/)).toBeNull()
    expect(screen.getByRole('img', { name: 'liusha' })).not.toHaveAttribute('title')
  })
})
