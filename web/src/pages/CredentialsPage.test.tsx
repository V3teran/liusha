import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CredentialsPage } from './CredentialsPage'
import { deleteCredentialIdentity, listCredentials, saveCredentialIdentity } from '@/api/credentials'
import type { CredentialIdentity, CredentialListItem } from '@/api/types'
import { ConfirmHost } from '@/components/ui/confirm'
import { Toaster } from '@/components/ui/toast'
import { MemoryRouter } from 'react-router-dom'

vi.mock('@/api/credentials', () => ({
  listCredentials: vi.fn(),
  saveCredentialIdentity: vi.fn(),
  deleteCredentialIdentity: vi.fn(),
}))

const mockedList = vi.mocked(listCredentials)
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

// 服务端的检索与分页在后端测试里锁定；这里用同口径的内存实现驱动页面。
const ROWS: CredentialListItem[] = [
  { host: 'a.com', ...makeIdentity() },
  {
    host: 'a.com',
    ...makeIdentity({
      username: 'task:t1:session',
      kind: 'session',
      task_id: 't1',
      task_status: 'active',
      credentials: [{ type: 'headers', key: 'Cookie', value: 'sess-t1-cookie' }],
    }),
  },
  {
    host: 'b.com',
    ...makeIdentity({ username: 'guest', role: 'guest', credentials: [{ type: 'query', key: 'token', value: 'guest-tk' }] }),
  },
]

function fakeList(search: string, page: number, size: number) {
  const q = search.toLowerCase()
  const hit = ROWS.filter((r) =>
    [r.host, r.username, r.role, ...r.credentials.map((c) => `${c.key}=${c.value}`)].some((f) => f.toLowerCase().includes(q)),
  )
  return Promise.resolve({ items: hit.slice((page - 1) * size, page * size), total: hit.length })
}

function renderPage(entry = '/credentials') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[entry]}>
        <CredentialsPage />
        <ConfirmHost />
        <Toaster />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockedList.mockImplementation(fakeList)
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
    // 计数 = 服务端给的匹配总数（2 个 host 共 3 个身份）
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
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const delBtn = await screen.findByRole('button', { name: '删除用户名 task:t1:session 的凭证' })
    await user.click(delBtn)
    expect(mockedDeleteIdentity).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: '确认删除' }))

    await waitFor(() =>
      expect(mockedDeleteIdentity).toHaveBeenCalledWith('a.com', 'task:t1:session'),
    )
  })

  it('每页条数、页码与搜索词取自 URL，原样交给服务端', async () => {
    renderPage('/credentials?search=guest-tk&size=10')
    await waitFor(() => expect(countText('共 1 条')).toBeTruthy())
    expect(mockedList).toHaveBeenLastCalledWith('guest-tk', 1, 10)
    expect((screen.getByRole('combobox', { name: '每页条数' }) as HTMLSelectElement).value).toBe('10')
    expect((screen.getByPlaceholderText('搜索主机 / 用户名 / 键 / 值…') as HTMLInputElement).value).toBe('guest-tk')
  })

  it('页码越界（如删掉末页最后一条）退回最后一页', async () => {
    renderPage('/credentials?size=10&page=3')
    expect(await screen.findByText(`Cookie: ${PLAINTEXT}`)).toBeTruthy()
    expect(mockedList).toHaveBeenLastCalledWith('', 1, 10)
  })

  it('加载失败显示错误态，重试重新请求', async () => {
    mockedList.mockRejectedValueOnce(new Error('凭证库不可用'))
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('凭证库不可用')
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText(`Cookie: ${PLAINTEXT}`)).toBeTruthy()
  })

  it('搜索栏可用：按凭证值过滤且不崩溃', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText(`Cookie: ${PLAINTEXT}`)

    const box = screen.getByPlaceholderText('搜索主机 / 用户名 / 键 / 值…')
    await user.type(box, 'guest-tk')
    // 停顿后交给服务端检索：只剩 b.com/guest 身份，计数是匹配总数
    await waitFor(() => expect(countText('共 1 条')).toBeTruthy())
    expect(mockedList).toHaveBeenLastCalledWith('guest-tk', 1, 50)
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

})
