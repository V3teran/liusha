import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { FilePlus2, FolderOpen, RefreshCw, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import {
  deleteWorkspacePath,
  getWorkspaceFile,
  getWorkspaceTree,
  listWorkspaceTasks,
  mkdirWorkspace,
  putWorkspaceFile,
  type WorkspaceEntry,
} from '@/api/workspaces'
import { cn } from '@/lib/utils'

/**
 * 工作空间页：沙盒任务工件的宿主持久层（work/out/profile）的文件 CRUD。
 * 任务选单 → 文件树 → 查看/编辑/保存、新建（文件/目录）、删除。
 * 二进制文件只读展示元信息；编辑保存以原始字节直传（二进制安全）。
 */
export function WorkspacePage() {
  const qc = useQueryClient()
  const tasksQuery = useQuery({ queryKey: ['workspace', 'tasks'], queryFn: listWorkspaceTasks })
  const tasks = tasksQuery.data ?? []
  const [activeTask, setActiveTask] = useState<string | null>(null)

  const treeQuery = useQuery({
    queryKey: ['workspace', 'tree', activeTask],
    queryFn: () => getWorkspaceTree(activeTask!),
    enabled: !!activeTask,
    placeholderData: keepPreviousData,
  })
  const entries = useMemo(
    () => (treeQuery.data ?? []).filter((e) => !e.is_dir),
    [treeQuery.data],
  )
  const [selected, setSelected] = useState<string | null>(null)

  const fileQuery = useQuery({
    queryKey: ['workspace', 'file', activeTask, selected],
    queryFn: () => getWorkspaceFile(activeTask!, selected!),
    enabled: !!activeTask && !!selected,
  })
  const [draft, setDraft] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [notice, setNotice] = useState('')

  const editing = draft !== null
  const dirty = fileQuery.data ? draft !== null && draft !== fileQuery.data.content : false

  function refresh() {
    void qc.invalidateQueries({ queryKey: ['workspace'] })
  }

  async function save() {
    if (!activeTask || !selected || draft === null) return
    await putWorkspaceFile(activeTask, selected, new TextEncoder().encode(draft))
    setDraft(null)
    setNotice(`已保存 ${selected}`)
    refresh()
  }

  async function createFile() {
    if (!activeTask || !newName.trim()) return
    await putWorkspaceFile(activeTask, newName.trim(), new Uint8Array())
    setNewName('')
    setSelected(newName.trim())
    setDraft('')
    setNotice(`已创建 ${newName.trim()}`)
    refresh()
  }

  async function createDir() {
    if (!activeTask || !newName.trim()) return
    await mkdirWorkspace(activeTask, newName.trim())
    setNewName('')
    setNotice(`已创建目录 ${newName.trim()}`)
    refresh()
  }

  async function remove(path: string) {
    if (!activeTask || !window.confirm(`删除 ${path}？（目录递归删除，不可恢复）`)) return
    await deleteWorkspacePath(activeTask, path)
    if (selected === path) {
      setSelected(null)
      setDraft(null)
    }
    setNotice(`已删除 ${path}`)
    refresh()
  }

  return (
    <div className="mx-auto w-full max-w-6xl px-6 py-8">
      <header className="mb-6 flex items-center gap-3">
        <FolderOpen className="h-6 w-6 text-muted" />
        <div>
          <h1 className="text-lg font-semibold">工作空间</h1>
          <p className="text-sm text-muted">
            沙盒任务工件的宿主持久层——脚本、扫描输出、复现材料（work/out/profile）
          </p>
        </div>
      </header>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <select
          value={activeTask ?? ''}
          onChange={(e) => {
            setActiveTask(e.target.value || null)
            setSelected(null)
            setDraft(null)
          }}
          className="rounded-lg border border-border bg-surface px-2 py-1.5 text-sm text-text"
        >
          <option value="">选择任务…</option>
          {tasks.map((t) => (
            <option key={t.name} value={t.name}>{t.name}</option>
          ))}
        </select>
        <button
          onClick={() => refresh()}
          className="flex items-center gap-1 rounded-lg border border-border px-2 py-1.5 text-sm text-muted hover:text-text"
        >
          <RefreshCw className="h-3.5 w-3.5" /> 刷新
        </button>
        {notice && <span className="text-xs text-muted">{notice}</span>}
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[280px_1fr]">
        {/* 文件树 */}
        <div className="rounded-2xl border border-border bg-surface p-3">
          {!activeTask ? (
            <p className="p-4 text-sm text-muted">先选择一个任务工作区</p>
          ) : (
            <>
              <FileTree
                entries={entries}
                selected={selected}
                onSelect={(p) => {
                  setSelected(p)
                  setDraft(null)
                }}
              />
              <div className="mt-3 flex items-center gap-2 border-t border-border pt-3">
                <input
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="新路径，如 work/poc.py"
                  className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1 text-xs"
                />
                <button onClick={() => void createFile()} title="新建文件" className="text-muted hover:text-text">
                  <FilePlus2 className="h-4 w-4" />
                </button>
                <button onClick={() => void createDir()} title="新建目录" className="text-muted hover:text-text">
                  <FolderOpen className="h-4 w-4" />
                </button>
              </div>
            </>
          )}
        </div>

        {/* 查看 / 编辑 */}
        <div className="rounded-2xl border border-border bg-surface p-3">
          {!selected ? (
            <p className="p-4 text-sm text-muted">从左侧选择文件</p>
          ) : fileQuery.isLoading ? (
            <p className="p-4 text-sm text-muted">加载中…</p>
          ) : fileQuery.data?.binary ? (
            <div className="space-y-2 p-2">
              <div className="flex items-center gap-2">
                <Badge size="sm" color="neutral">二进制</Badge>
                <span className="text-sm text-text">{selected}</span>
                <span className="text-xs text-muted">{fileQuery.data.size} 字节（base64 预览禁用编辑）</span>
              </div>
              <textarea
                readOnly
                value={fileQuery.data.content.slice(0, 2000)}
                className="h-72 w-full rounded-lg border border-border bg-surface-2 p-2 font-mono text-xs"
              />
              <button
                onClick={() => void remove(selected)}
                className="flex items-center gap-1 text-xs text-muted hover:text-text"
              >
                <Trash2 className="h-3.5 w-3.5" /> 删除
              </button>
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-sm text-text">{selected}</span>
                <div className="flex items-center gap-2">
                  {dirty && <Badge size="sm" color="neutral">未保存</Badge>}
                  <button
                    onClick={() => void save()}
                    disabled={!editing}
                    className={cn(
                      'rounded-lg border border-border px-2.5 py-1 text-xs',
                      editing ? 'text-text hover:bg-surface-2' : 'cursor-not-allowed text-muted opacity-50',
                    )}
                  >
                    保存
                  </button>
                  <button
                    onClick={() => void remove(selected)}
                    className="flex items-center gap-1 text-xs text-muted hover:text-text"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> 删除
                  </button>
                </div>
              </div>
              {fileQuery.data?.truncated && (
                <p className="text-xs text-muted">文件超过 4MiB，仅载入前段；保存将覆盖全文，请谨慎</p>
              )}
              <textarea
                value={draft ?? fileQuery.data?.content ?? ''}
                onChange={(e) => setDraft(e.target.value)}
                spellCheck={false}
                className="h-96 w-full rounded-lg border border-border bg-surface-2 p-3 font-mono text-xs leading-5"
              />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** 扁平文件树：按服务端排序（目录前、路径字典序），深度即缩进。 */
function FileTree({
  entries,
  selected,
  onSelect,
}: {
  entries: WorkspaceEntry[]
  selected: string | null
  onSelect: (path: string) => void
}) {
  return (
    <ul className="space-y-0.5 text-sm">
      {entries.map((e) => {
        const depth = e.path.split('/').length - (e.is_dir ? 1 : 0)
        return (
          <li key={e.path}>
            <button
              onClick={() => onSelect(e.path)}
              style={{ paddingLeft: `${depth * 14 + 8}px` }}
              className={cn(
                'flex w-full items-center gap-1.5 rounded-lg py-1 text-left hover:bg-surface-2',
                selected === e.path && 'bg-surface-2 text-text',
                !e.is_dir && 'text-text',
                e.is_dir && 'text-muted',
              )}
            >
              <span className="truncate font-mono text-xs">{e.path}</span>
            </button>
          </li>
        )
      })}
      {entries.length === 0 && <li className="p-2 text-xs text-muted">空工作区</li>}
    </ul>
  )
}
