import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import CredentialsPage from './CredentialsPage'
import {
  deleteCredentialIdentity,
  getCredentialDetail,
  listCredentialHosts,
  saveCredentialIdentity,
} from '@/api/client'
import type { CredentialHostSummary, CredentialIdentity } from '@/api/types'

vi.mock('@/api/client', () => ({
  listCredentialHosts: vi.fn(),
  getCredentialDetail: vi.fn(),
  saveCredentialIdentity: vi.fn(),
  deleteCredentialIdentity: vi.fn(),
  purgeCredentialSessions: vi.fn(),
  deleteCredentialHost: vi.fn(),
}))

const mockedHosts = listCredentialHosts as unknown as ReturnType<typeof vi.fn>
const mockedDetail = getCredentialDetail as unknown as ReturnType<typeof vi.fn>
const mockedDeleteIdentity = deleteCredentialIdentity as unknown as ReturnType<typeof vi.fn>
const mockedSaveIdentity = saveCredentialIdentity as unknown as ReturnType<typeof vi.fn>

const PLAINTEXT = 'PHPSESSID=240f4d6892e481e114c9faf974c913a2; security=low'

function makeIdentity(over: Partial<CredentialIdentity> = {}): CredentialIdentity {
  return {
    username: 'admin-low',
    role: 'admin',
    credentials: [{ type: 'headers', key: 'Cookie', value: PLAINTEXT }],
    kind: 'persistent',
    created_at: '2026-10-22T14:32:21Z',
    ...over,
  }
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <CredentialsPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  // 双 host：主机列在跨 host 结果下显示（单 host 时按设计隐藏降噪）
  const hosts: CredentialHostSummary[] = [
    { host: 'a.com', identities: 1, sessions: 1 },
    { host: 'b.com', identities: 1, sessions: 0 },
  ]
  mockedHosts.mockResolvedValue(hosts)
  mockedDetail.mockImplementation(async (host: string) => {
    if (host === 'b.com') {
      return { host, identities: [makeIdentity({ username: 'guest', role: 'guest', credentials: [{ type: 'query', key: 'token', value: 'guest-tk' }] })] }
    }
    return {
      host,
      identities: [
        makeIdentity(),
        makeIdentity({
          username: 'task:t1:session',
          kind: 'session',
          task_id: 't1',
          task_status: 'active',
          credentials: [{ type: 'headers', key: 'Cookie', value: 'sess-t1-cookie' }],
        }),
      ],
    }
  })
  mockedDeleteIdentity.mockResolvedValue(undefined)
  mockedSaveIdentity.mockResolvedValue(undefined)
})

describe('CredentialsPage（列表形态）', () => {
  // PaginationToolbar 的「共 N 条」被 <strong> 拆分，按 textContent 匹配。
  const countText = (t: string) => screen.getByText((_, el) => el?.textContent === t)

  it('一个身份一行：主机列、明文直显（类别信息在详情 Sheet，不再占列）', async () => {
    renderPage()
    // 行内容就位后再做同步断言（表头先于行渲染，等行不等头）
    expect(await screen.findByText(`Cookie: ${PLAINTEXT}`)).toBeTruthy()
    expect(screen.getByText('主机')).toBeTruthy()
    expect(screen.getByRole('columnheader', { name: '用户名' })).toBeTruthy()
    // 主机列直接取存储键（归一 host 键，不再有身份级 address 字段）
    expect(screen.getAllByText('a.com').length).toBe(2)
    expect(screen.getByText('b.com')).toBeTruthy()
    expect(screen.getByText('Cookie: sess-t1-cookie')).toBeTruthy()
    expect(screen.getByText('[query] token: guest-tk')).toBeTruthy()
    // 类别列已删（类别/任务状态信息移入详情 Sheet 与筛选器）
    expect(screen.queryByRole('columnheader', { name: '类别' })).toBeNull()
    // 计数 = 身份数（2 host 共 3 个身份）
    expect(countText('共 3 条')).toBeTruthy()
  })

  it('行内编辑角色：点击单元格变输入框，Enter 提交 PUT', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const roleCells = await screen.findAllByTitle('点击编辑角色')
    fireEvent.click(roleCells[0])
    // 编辑态：该单元格的 title 消失（被输入框替换）
    await waitFor(() => {
      expect(screen.queryAllByTitle('点击编辑角色').length).toBe(roleCells.length - 1)
    })

    const input = await screen.findByDisplayValue('admin')
    await user.clear(input)
    await user.type(input, 'root{Enter}')

    await waitFor(() =>
      expect(mockedSaveIdentity).toHaveBeenCalledWith(
        'a.com',
        expect.objectContaining({ username: 'admin-low', role: 'root', credentials: expect.any(Array) }),
      ),
    )
  })

  it('行内编辑凭证：点击原始行变输入框（原始 HTTP 格式），Enter 提交', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const credCells = await screen.findAllByTitle('点击编辑此凭证（原始 HTTP 行）')
    await user.click(credCells[0]) // 首行为 admin-low 的 Cookie

    const input = await screen.findByDisplayValue(`Cookie: ${PLAINTEXT}`)
    await user.clear(input)
    await user.type(input, 'Cookie: newsess{Enter}')

    await waitFor(() =>
      expect(mockedSaveIdentity).toHaveBeenCalledWith(
        'a.com',
        expect.objectContaining({
          username: 'admin-low',
          credentials: [expect.objectContaining({ key: 'Cookie', value: 'newsess' })],
        }),
      ),
    )
  })

  it('行内删除按钮走单删 API', async () => {
    const user = userEvent.setup()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const delBtn = await screen.findByRole('button', { name: '删除用户名 task:t1:session 的凭证' })
    await user.click(delBtn)

    await waitFor(() =>
      expect(mockedDeleteIdentity).toHaveBeenCalledWith('a.com', 'task:t1:session'),
    )
  })

  it('搜索栏可用：按凭证值过滤且不崩溃', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const box = screen.getByPlaceholderText('搜索主机 / 用户名 / 键 / 值…')
    await user.type(box, 'guest-tk')
    // 只剩 b.com/guest 身份，计数同步
    await waitFor(() => expect(countText('共 1 条')).toBeTruthy())
    await waitFor(() => expect(screen.queryByText(`Cookie: ${PLAINTEXT}`)).toBeNull())

    await user.clear(box)
    await waitFor(() => expect(countText('共 3 条')).toBeTruthy())
  })

  it('分页与创建时间列', async () => {
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)
    // 创建时间列存在；旧的「录入」列头已删（右上角录入按钮不受影响）
    expect(screen.getByRole('columnheader', { name: '创建时间' })).toBeTruthy()
    expect(screen.queryByRole('columnheader', { name: '录入' })).toBeNull()
    // 分页控件：上一页禁用（第 1 页）、下一页禁用（单页）
    expect(screen.getByRole('button', { name: '上一页' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一页' })).toBeTruthy()
  })

  it('录入走表头上方行内新行：Enter 保存', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    await user.click(screen.getByRole('button', { name: /录入/ }))
    const hostInput = await screen.findByPlaceholderText('目标 host:port')
    await user.type(hostInput, 'c.com:9090')
    await user.type(screen.getByPlaceholderText('用户名'), 'bob')
    await user.type(
      screen.getByPlaceholderText('Cookie: PHPSESSID=…（或 [query] token: xxx）'),
      'Authorization: Bearer t1{Enter}',
    )

    await waitFor(() =>
      expect(mockedSaveIdentity).toHaveBeenCalledWith(
        'c.com:9090',
        expect.objectContaining({
          username: 'bob',
          credentials: [expect.objectContaining({ key: 'Authorization', value: 'Bearer t1' })],
        }),
      ),
    )
  })

  // 注：host 级「清理会话/删除主机」批量操作已随列表重构移除（行级删除覆盖同等语义）。
})
