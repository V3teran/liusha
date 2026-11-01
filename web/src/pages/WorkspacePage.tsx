import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Download,
  FilePlus2,
  FolderOpen,
  FolderPlus,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-react'
import {
  deleteWorkspacePath,
  downloadWorkspacePath,
  getWorkspaceFile,
  getWorkspaceTree,
  mkdirWorkspace,
  putWorkspaceFile,
  type WorkspaceEntry,
} from '@/api/workspaces'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

/**
 * 工作空间页：沙盒任务工件的宿主持久层（work/out/profile）的文件 CRUD。
 * 直接展示整棵工作区文件结构（顶层即任务目录，最新在前），搜索框过滤路径；
 * 行悬停露下载/删除，工具栏新建文件/目录，文件点开可编辑保存与下载。
 * 二进制文件只读展示元信息；编辑保存以原始字节直传（二进制安全）。
 */
export function WorkspacePage() {
  const qc = useQueryClient()
  const [taskQuery, setTaskQuery] = useState('')
  const treeQuery = useQuery({
    queryKey: ['workspace', 'tree'],
    queryFn: getWorkspaceTree,
    placeholderData: keepPreviousData,
  })
  const allEntries = treeQuery.data ?? []
  // eslint-disable-next-line no-console
  console.log('WS_RENDER:', JSON.stringify({ status: treeQuery.status, fetch: treeQuery.fetchStatus, n: allEntries.length, el: !!document.querySelector('ul') }))

  // 搜索过滤树：命中节点 + 祖先链保留（树形上下文不因过滤断裂）
  const entries = useMemo(() => {
    const q = taskQuery.trim().toLowerCase()
    if (!q) return allEntries
    const hit = new Set<string>()
    for (const e of allEntries) {
      if (e.path.toLowerCase().includes(q)) {
        hit.add(e.path)
        const segs = e.path.split('/')
        for (let i = 1; i < segs.length; i++) hit.add(segs.slice(0, i).join('/') + '/')
      }
    }
    return allEntries.filter((e) => hit.has(e.path))
  }, [allEntries, taskQuery])

  const [selected, setSelected] = useState<string | null>(null)
  const fileQuery = useQuery({
    queryKey: ['workspace', 'file', selected],
    queryFn: () => getWorkspaceFile(selected!),
    enabled: !!selected,
  })
  const [draft, setDraft] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [notice, setNotice] = useState('')

  const dirty = fileQuery.data ? draft !== null && draft !== fileQuery.data.content : false
  const editing = draft !== null

  function refresh() {
    void qc.invalidateQueries({ queryKey: ['workspace'] })
  }

  async function save() {
    // eslint-disable-next-line no-console
    console.log('SAVE_DRAFT:', JSON.stringify({ selected, draft }))
    if (!selected || draft === null) return
    await putWorkspaceFile(selected, new TextEncoder().encode(draft))
    setDraft(null)
    setNotice(`已保存 ${selected}`)
    refresh()
  }

  async function createFile() {
    if (!newName.trim()) return
    try {
      await putWorkspaceFile(newName.trim(), new Uint8Array())
      setNotice(`已创建 ${newName.trim()}`)
      setNewName('')
      setSelected(newName.trim())
      refresh()
    } catch (e) {
      setNotice(`创建失败：${(e as Error).message}`)
    }
  }

  async function createDir() {
    if (!newName.trim()) return
    try {
      await mkdirWorkspace(newName.trim())
      setNotice(`已创建目录 ${newName.trim()}`)
      setNewName('')
      refresh()
    } catch (e) {
      setNotice(`创建失败：${(e as Error).message}`)
    }
  }

  async function download(path: string) {
    try {
      await downloadWorkspacePath(path)
      setNotice(`已下载 ${path}`)
    } catch (e) {
      setNotice(`下载失败：${(e as Error).message}`)
    }
  }

  async function remove(path: string) {
    if (!window.confirm(`删除 ${path}？（目录递归删除，不可恢复）`)) return
    try {
      await deleteWorkspacePath(path)
      if (selected === path) {
        setSelected(null)
        setDraft(null)
      }
      setNotice(`已删除 ${path}`)
      refresh()
    } catch (e) {
      setNotice(`删除失败：${(e as Error).message}`)
    }
  }

  const taskCount = allEntries.filter((e) => e.depth === 0 && e.is_dir).length

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
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <input
            value={taskQuery}
            onChange={(e) => setTaskQuery(e.target.value)}
            placeholder="搜索路径（任务目录 / 文件名）…"
            spellCheck={false}
            aria-label="搜索工作区路径"
            className="h-9 w-72 rounded-lg border border-border bg-surface pl-10 pr-3 text-sm text-text outline-none transition-all placeholder:text-text-tertiary focus:border-accent"
          />
        </div>
        <button
          onClick={() => refresh()}
          className="flex items-center gap-1 rounded-lg border border-border px-2 py-1.5 text-sm text-muted hover:text-text"
        >
          <RefreshCw className="h-3.5 w-3.5" /> 刷新
        </button>
        {allEntries.length > 0 && (
          <span className="text-xs text-muted opacity-75">{taskCount} 个任务工作区</span>
        )}
        {notice && <span className="text-xs text-muted">{notice}</span>}
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[320px_1fr]">
        {/* 文件树 + 新建工具栏 */}
        <div className="flex min-h-0 flex-col rounded-2xl border border-border bg-surface p-3">
          <div className="mb-3 flex items-center gap-2 border-b border-border pb-2">
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="新路径，如 task/work/poc.py"
              spellCheck={false}
              aria-label="新建路径"
              className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1 text-xs"
            />
            <button onClick={() => void createFile()} title="新建文件" aria-label="新建文件" className="text-muted hover:text-text">
              <FilePlus2 className="h-4 w-4" />
            </button>
            <button onClick={() => void createDir()} title="新建目录" aria-label="新建目录" className="text-muted hover:text-text">
              <FolderPlus className="h-4 w-4" />
            </button>
            <button onClick={() => refresh()} title="刷新" aria-label="刷新" className="text-muted hover:text-text">
              <RefreshCw className="h-4 w-4" />
            </button>
          </div>
          <FileTree
            entries={entries}
            selected={selected}
            onSelect={(p) => {
              setSelected(p)
              setDraft(null)
            }}
            onDownload={(p) => void download(p)}
            onDelete={(p) => void remove(p)}
          />
        </div>

        {/* 查看 / 编辑 */}
        <div className="rounded-2xl border border-border bg-surface p-3">
          {!selected ? (
            <p className="p-4 text-sm text-muted">从左侧选择文件（行悬停可下载 / 删除）</p>
          ) : fileQuery.isLoading ? (
            <p className="p-4 text-sm text-muted">加载中…</p>
          ) : fileQuery.data?.binary ? (
            <div className="space-y-2 p-2">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Badge size="sm" color="neutral">二进制</Badge>
                  <span className="text-sm text-text">{selected}</span>
                  <span className="text-xs text-muted">{fileQuery.data.size} 字节（base64 预览禁用编辑）</span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => void download(selected)}
                    className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-muted hover:text-text"
                  >
                    <Download className="h-3.5 w-3.5" /> 下载
                  </button>
                  <button
                    onClick={() => void remove(selected)}
                    className="flex items-center gap-1 text-xs text-muted hover:text-text"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> 删除
                  </button>
                </div>
              </div>
              <textarea
                readOnly
                aria-label="文件内容预览"
                value={fileQuery.data.content.slice(0, 2000)}
                className="h-72 w-full rounded-lg border border-border bg-surface-2 p-2 font-mono text-xs"
              />
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-sm text-text">{selected}</span>
                <div className="flex items-center gap-2">
                  {dirty && <Badge size="sm" color="neutral">未保存</Badge>}
                  <button
                    onClick={() => void download(selected)}
                    className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-muted hover:text-text"
                  >
                    <Download className="h-3.5 w-3.5" /> 下载
                  </button>
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
                aria-label="文件内容编辑器"
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

/** 扁平文件树：服务端排序（顶层任务目录最新在前，组内目录前、路径序），
 * 深度即缩进。目录为弱化行，文件行可选中；行悬停露下载/删除。 */
function FileTree({
  entries,
  selected,
  onSelect,
  onDownload,
  onDelete,
}: {
  entries: WorkspaceEntry[]
  selected: string | null
  onSelect: (path: string) => void
  onDownload: (path: string) => void
  onDelete: (path: string) => void
}) {
  return (
    <ul className="min-h-0 flex-1 space-y-0.5 overflow-y-auto text-sm">
      {entries.map((e) => {
        const depth = e.path.split('/').length - (e.is_dir ? 1 : 0)
        const base = e.path.split('/').pop() || e.path
        return (
          <li key={e.path} className="group/row">
            <div
              style={{ paddingLeft: `${depth * 14 + 8}px` }}
              className={cn(
                'flex items-center gap-1.5 rounded-lg py-1 text-left',
                !e.is_dir && 'cursor-pointer hover:bg-surface-2',
                !e.is_dir && selected === e.path && 'bg-surface-2 text-text',
                e.is_dir && 'text-muted',
              )}
              onClick={() => !e.is_dir && onSelect(e.path)}
            >
              {e.is_dir ? (
                <FolderOpen className="h-3.5 w-3.5 flex-shrink-0 opacity-60" />
              ) : (
                <span className="w-3.5 flex-shrink-0" />
              )}
              <span className="min-w-0 flex-1 truncate font-mono text-xs">{base}</span>
              <span className="flex flex-shrink-0 items-center gap-1 opacity-0 transition-opacity group-focus-within/row:opacity-100 group-hover/row:opacity-100">
                <button
                  aria-label={`下载 ${e.path}`}
                  title={`下载${e.is_dir ? '（zip）' : ''}`}
                  onClick={(ev) => {
                    ev.stopPropagation()
                    onDownload(e.path)
                  }}
                  className="text-muted hover:text-text"
                >
                  <Download className="h-3.5 w-3.5" />
                </button>
                <button
                  aria-label={`删除 ${e.path}`}
                  title="删除"
                  onClick={(ev) => {
                    ev.stopPropagation()
                    onDelete(e.path)
                  }}
                  className="text-muted hover:text-text"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </span>
            </div>
          </li>
        )
      })}
      {entries.length === 0 && <li className="p-2 text-xs text-muted">无匹配路径</li>}
    </ul>
  )
}
