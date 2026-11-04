import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { LoginPage } from './LoginPage'
import * as authApi from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/auth', () => ({
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
  changePassword: vi.fn(),
}))

const mMe = authApi.me as unknown as ReturnType<typeof vi.fn>
const mLogin = authApi.login as unknown as ReturnType<typeof vi.fn>

function renderAtLogin() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<div>HOME-PAGE</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('LoginPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.getState().clear()
    mMe.mockRejectedValue(new Error('GET 401'))
  })
  it('登录失败内联展示后端错误', async () => {
    mLogin.mockRejectedValue(new Error('POST 401: 用户名或密码错误'))
    renderAtLogin()

    const user = userEvent.setup()
    await waitFor(() => expect(screen.getByLabelText('用户名')).toBeInTheDocument())
    expect(screen.queryByLabelText('确认密码')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'wrong-pass-x')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或密码错误')
    expect(mLogin).toHaveBeenCalledWith('admin', 'wrong-pass-x')
  })
  it('登录成功写入账号态并跳主页', async () => {
    mLogin.mockResolvedValue({ username: 'admin' })
    renderAtLogin()

    const user = userEvent.setup()
    await waitFor(() => expect(screen.getByLabelText('用户名')).toBeInTheDocument())
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'correct-pass')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => expect(screen.getByText('HOME-PAGE')).toBeInTheDocument())
    expect(useAuthStore.getState().username).toBe('admin')
  })
  it('后端不可达时展示统一连接提示，不裸露浏览器报错', async () => {
    mLogin.mockRejectedValue(new TypeError('Failed to fetch'))
    renderAtLogin()

    const user = userEvent.setup()
    await waitFor(() => expect(screen.getByLabelText('用户名')).toBeInTheDocument())
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'whatever-x')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('无法连接到服务器')
  })
  it('已登录访问 /login 直接回主页', async () => {
    mMe.mockResolvedValue({ username: 'admin' })
    renderAtLogin()

    await waitFor(() => expect(screen.getByText('HOME-PAGE')).toBeInTheDocument())
  })
})
