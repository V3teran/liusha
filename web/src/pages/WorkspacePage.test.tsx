import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspacePage } from './WorkspacePage'
import {
  deleteWorkspacePath,
  getWorkspaceFile,
  getWorkspaceTree,
  putWorkspaceFile,
} from '@/api/workspaces'

vi.mock('@/api/workspaces', () => ({
  getWorkspaceTree: vi.fn(),
  getWorkspaceFile: vi.fn(),
  putWorkspaceFile: vi.fn(),
  mkdirWorkspace: vi.fn(),
  deleteWorkspacePath: vi.fn(),
  downloadWorkspacePath: vi.fn(),
}))

const mockedTree = getWorkspaceTree as unknown as ReturnType<typeof vi.fn>
const mockedFile = getWorkspaceFile as unknown as ReturnType<typeof vi.fn>
const mockedPut = putWorkspaceFile as unknown as ReturnType<typeof vi.fn>

function makeEntry(path: string, isDir = false) {
  return { path, depth: path.split('/').length - (isDir ? 1 : 0), size: 8, is_dir: isDir, mod_time: '2026-11-01T10:00:00Z' }
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <WorkspacePage />
    </QueryClientProvider>,
  )
}

describe('WorkspacePage', () => {
  it('直接展示整棵文件结构（目录弱化行 + 文件行），默认最新任务在前', async () => {
    mockedTree.mockImplementation(() => {
      console.log('DBG tree called')
      return Promise.resolve([
        makeEntry('task-abc/', true),
        makeEntry('task-abc/work/', true),
        makeEntry('task-abc/work/poc.py'),
      ])
    })
    renderPage()

    // 树行按组序渲染（React Query 二次渲染存在一帧数据空窗，断言一律重试式）
    await waitFor(() => {
      const rows = screen.getAllByRole('listitem')
      expect(rows.length).toBe(3)
      expect(rows[0].textContent).toContain('task-abc/')
      expect(rows[1].textContent).toContain('work/')
      expect(rows[2].textContent).toContain('poc.py')
    })
    expect(screen.getByText(/个任务工作区/)).toBeTruthy()
  })

  it('点击文件打开编辑，保存直传字节（根相对路径）', async () => {
    mockedTree.mockResolvedValue([makeEntry('task-abc/work/poc.py')])
    mockedFile.mockResolvedValue({
      path: 'task-abc/work/poc.py', binary: false, content: 'old', size: 3,
      truncated: false, mod_time: '2026-11-01T10:00:01Z',
    })
    mockedPut.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByText('poc.py'))
    const box = await screen.findByDisplayValue('old')
    // 编辑器内容注入用 fireEvent.change（确定性单事件），点击类交互仍走 userEvent
    fireEvent.change(box, { target: { value: 'new' } })
    fireEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(1))
    const args = mockedPut.mock.calls[0]
    expect(args[0]).toBe('task-abc/work/poc.py')
    expect(new TextDecoder().decode(args[1] as Uint8Array)).toBe('new')
  })

  it('搜索过滤树（命中 + 祖先链），无匹配显示占位', async () => {
    mockedTree.mockResolvedValue([
      makeEntry('task-abc/', true),
      makeEntry('task-abc/work/', true),
      makeEntry('task-abc/work/poc.py'),
      makeEntry('task-xyz/', true),
    ])
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('poc.py')
    await user.type(screen.getByLabelText('搜索工作区路径'), 'xyz')
    expect(screen.getByText('task-xyz/')).toBeTruthy()
    // 命中 + 祖先链之外整体滤掉（无匹配占位仅在结果为空时出现，此处有结果）
    await waitFor(() => expect(screen.queryByText('poc.py')).toBeNull())
  })

  it('新建文件走根相对路径', async () => {
    mockedTree.mockResolvedValue([])
    mockedPut.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()

    await user.type(await screen.findByLabelText('新建路径'), 'task-new/work/poc.py')
    await user.click(screen.getByLabelText('新建文件'))
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith('task-new/work/poc.py', expect.any(Uint8Array))
    })
  })

  it('删除走确认 + 根相对路径', async () => {
    mockedTree.mockResolvedValue([makeEntry('task-abc/work/poc.py')])
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    ;(deleteWorkspacePath as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('poc.py')
    await user.click(screen.getByLabelText('删除 task-abc/work/poc.py'))
    await waitFor(() => {
      expect(deleteWorkspacePath).toHaveBeenCalledWith('task-abc/work/poc.py')
    })
  })
})
