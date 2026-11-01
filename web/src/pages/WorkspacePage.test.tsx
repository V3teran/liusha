import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspacePage } from './WorkspacePage'
import {
  listWorkspaceTasks,
  getWorkspaceTree,
  getWorkspaceFile,
  putWorkspaceFile,
  deleteWorkspacePath,
} from '@/api/workspaces'

vi.mock('@/api/workspaces', () => ({
  listWorkspaceTasks: vi.fn(),
  getWorkspaceTree: vi.fn(),
  getWorkspaceFile: vi.fn(),
  putWorkspaceFile: vi.fn(),
  deleteWorkspacePath: vi.fn(),
  mkdirWorkspace: vi.fn(),
}))

const mockedList = listWorkspaceTasks as unknown as ReturnType<typeof vi.fn>
const mockedTree = getWorkspaceTree as unknown as ReturnType<typeof vi.fn>
const mockedFile = getWorkspaceFile as unknown as ReturnType<typeof vi.fn>
const mockedPut = putWorkspaceFile as unknown as ReturnType<typeof vi.fn>

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <WorkspacePage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockedList.mockResolvedValue([{ name: 'task-abc', mod_time: '2026-11-01T10:00:00Z' }])
})

describe('WorkspacePage', () => {
  it('选择任务后渲染文件树并可打开文件', async () => {
    mockedTree.mockResolvedValue([
      { path: 'work/', size: 96, is_dir: true, mod_time: '2026-11-01T10:00:00Z' },
      { path: 'work/poc.py', size: 10, is_dir: false, mod_time: '2026-11-01T10:00:01Z' },
    ])
    mockedFile.mockResolvedValue({
      path: 'work/poc.py', binary: false, content: 'print(1)', size: 8,
      truncated: false, mod_time: '2026-11-01T10:00:01Z',
    })
    const user = userEvent.setup()
    renderPage()

    // 等任务选项渲染（异步 query）再选择
    await screen.findByText('task-abc')
    await user.selectOptions(screen.getByRole('combobox'), 'task-abc')
    expect(await screen.findByText('work/poc.py')).toBeTruthy()
    await user.click(screen.getByText('work/poc.py'))
    expect(await screen.findByDisplayValue('print(1)')).toBeTruthy()
  })

  it('编辑后保存直传字节', async () => {
    mockedTree.mockResolvedValue([
      { path: 'work/poc.py', size: 8, is_dir: false, mod_time: '2026-11-01T10:00:01Z' },
    ])
    mockedFile.mockResolvedValue({
      path: 'work/poc.py', binary: false, content: 'old', size: 3,
      truncated: false, mod_time: '2026-11-01T10:00:01Z',
    })
    mockedPut.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('task-abc')
    await user.selectOptions(screen.getByRole('combobox'), 'task-abc')
    await user.click(await screen.findByText('work/poc.py'))
    const box = await screen.findByDisplayValue('old')
    await user.clear(box)
    await user.type(box, 'new')
    await user.click(screen.getByText('保存'))
    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(1))
    const args = mockedPut.mock.calls[0]
    expect(args[0]).toBe('task-abc')
    expect(args[1]).toBe('work/poc.py')
    expect(new TextDecoder().decode(args[2] as Uint8Array)).toBe('new')
  })

  it('二进制文件禁用编辑', async () => {
    mockedTree.mockResolvedValue([
      { path: 'out/blob.bin', size: 4, is_dir: false, mod_time: '2026-11-01T10:00:01Z' },
    ])
    mockedFile.mockResolvedValue({
      path: 'out/blob.bin', binary: true, content: '/5+A=', size: 4,
      truncated: false, mod_time: '2026-11-01T10:00:01Z',
    })
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('task-abc')
    await user.selectOptions(screen.getByRole('combobox'), 'task-abc')
    await user.click(await screen.findByText('out/blob.bin'))
    expect(await screen.findByText('二进制')).toBeTruthy()
    // 编辑器以 base64 只读预览（文本框家族里按值定位）
    const editor = screen.getByDisplayValue('/5+A=')
    expect(editor).toHaveProperty('readOnly', true)
  })
})
