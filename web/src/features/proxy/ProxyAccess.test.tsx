import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { ProxyAccess } from './ProxyAccess'
import { getProxyStatus } from '@/api/proxy'
import type { ProxyStatus } from '@/api/types'

vi.mock('@/api/proxy', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/proxy')>()),
  getProxyStatus: vi.fn(),
}))
const mockStatus = vi.mocked(getProxyStatus)

const ONLINE: ProxyStatus = {
  state: 'online',
  last_seen_at: new Date().toISOString(),
  started_at: new Date().toISOString(),
  last_request_at: new Date().toISOString(),
  public_addr: ':58588',
  ca_fingerprint: 'AB:CD:EF',
}

function renderAccess() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ProxyAccess />
    </QueryClientProvider>,
  )
}

async function openDialog() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: /^代理接入（代理/ }))
  return screen.findByRole('dialog', { name: '代理接入' })
}

describe('ProxyAccess', () => {
  it('运行中：按钮带状态，对话框给出完整代理地址、证书下载与指纹', async () => {
    mockStatus.mockResolvedValue(ONLINE)
    renderAccess()
    expect(await screen.findByRole('button', { name: '代理接入（代理运行中）' })).toBeInTheDocument()
    const dialog = await openDialog()
    // 只有端口时主机取控制台所在主机
    expect(within(dialog).getByText(`${window.location.hostname}:58588`)).toBeInTheDocument()
    const download = within(dialog).getByRole('link', { name: /下载根证书/ })
    expect(download).toHaveAttribute('href', '/api/proxy/ca.crt')
    expect(download).toHaveAttribute('download', 'liusha-ca.crt')
    expect(within(dialog).getByText('AB:CD:EF')).toBeInTheDocument()
    expect(within(dialog).getByText(/最近收到请求/)).toBeInTheDocument()
  })

  it('运行中但尚未收到请求：提示去设置浏览器代理', async () => {
    mockStatus.mockResolvedValue({ ...ONLINE, last_request_at: null })
    renderAccess()
    const dialog = await openDialog()
    expect(within(dialog).getByText(/尚未收到请求/)).toBeInTheDocument()
  })

  it('已停止：显示最后在线时间，证书仍可下载', async () => {
    mockStatus.mockResolvedValue({ ...ONLINE, state: 'offline' })
    renderAccess()
    const dialog = await openDialog()
    expect(within(dialog).getByText('代理已停止')).toBeInTheDocument()
    expect(within(dialog).getByText(/最后在线/)).toBeInTheDocument()
    expect(within(dialog).getByRole('link', { name: /下载根证书/ })).toBeInTheDocument()
  })

  it('从未运行过：没有地址与证书，提示启动 proxy', async () => {
    mockStatus.mockResolvedValue({
      state: 'offline', last_seen_at: null, started_at: null, last_request_at: null, public_addr: '', ca_fingerprint: '',
    })
    renderAccess()
    const dialog = await openDialog()
    expect(within(dialog).getByText('代理未启动')).toBeInTheDocument()
    expect(within(dialog).queryByRole('link', { name: /下载根证书/ })).toBeNull()
  })

  // 读不到状态时不显示状态灯，不能把「读不到」误报成「已停止」。
  it('读取状态失败：按钮不带状态，对话框说明原因', async () => {
    mockStatus.mockImplementation(() => Promise.reject(new Error('网络错误')))
    renderAccess()
    const user = userEvent.setup()
    // 失败后按钮不带状态
    await user.click(await screen.findByRole('button', { name: '代理接入' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('网络错误')
  })
})
