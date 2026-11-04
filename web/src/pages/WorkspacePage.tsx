import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import CodeMirror from '@uiw/react-codemirror'
import { python } from '@codemirror/lang-python'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { StreamLanguage } from '@codemirror/language'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  Download,
  FilePlus2,
  FolderPlus,
  RefreshCw,
  Trash2,
  X,
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

/** 按扩展名选语言高亮；无匹配返回空数组（纯文本）。 */
function languageExtensions(path: string) {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  switch (ext) {
    case 'py':
      return [python()]
    case 'js':
    case 'ts':
    case 'jsx':
    case 'tsx':
      return [javascript()]
    case 'json':
      return [json()]
    case 'md':
      return [markdown()]
    case 'sh':
    case 'bash':
      return [StreamLanguage.define(shell)]
    default:
      return []
  }
}
/**
 * 工作空间页：沙盒任务工件的宿主持久层（work/out/profile）的文件 CRUD。
 * 直接展示整棵工作区文件结构（顶层即任务目录，最新在前），搜索框过滤路径；
 * 行悬停露下载/删除，工具栏新建文件/目录，文件点开可编辑保存与下载。
 * 二进制文件只读展示元信息；编辑保存以原始字节直传（二进制安全）。
 */
export function WorkspacePage() {
  const qc = useQueryClient()
  const [taskQuery, setTaskQuery] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const treeQuery = useQuery({
    queryKey: ['workspace', 'tree'],
    queryFn: getWorkspaceTree,
    placeholderData: keepPreviousData,
  })
  const allEntries = treeQuery.data ?? []
  // eslint-disable-next-line no-console
  console.log('WS_RENDER:', JSON.stringify({ status: treeQuery.status, fetch: treeQuery.fetchStatus, n: allEntries.length, el: !!document.querySelector('ul') }))

  // 树的可见节点 = 搜索过滤 ∩ 折叠过滤。搜索时无视折叠（命中即展开，
  // 否则搜到的文件被藏进折叠目录里等于搜索失效）；浏览时应用折叠。
  const entries = useMemo(() => {
    const q = taskQuery.trim().toLowerCase()
    let list = allEntries
    if (q) {
      const hit = new Set<string>()
      for (const e of allEntries) {
        if (e.path.toLowerCase().includes(q)) {
          hit.add(e.path)
          const segs = e.path.split('/')
          for (let i = 1; i < segs.length; i++) hit.add(segs.slice(0, i).join('/') + '/')
        }
      }
      list = allEntries.filter((e) => hit.has(e.path))
    } else if (collapsed.size > 0) {
      list = allEntries.filter(
        (e) => ![...collapsed].some((d) => e.path.startsWith(d) && e.path !== d),
      )
    }
    return list
  }, [allEntries, taskQuery, collapsed])

  function toggleDir(dirPath: string) {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(dirPath)) next.delete(dirPath)
      else next.add(dirPath)
      return next
    })
  }

  function collapseAll() {
    setCollapsed(new Set(allEntries.filter((e) => e.is_dir).map((e) => e.path)))
  }

  const [selected, setSelected] = useState<string | null>(null)
  const fileQuery = useQuery({
    queryKey: ['workspace', 'file', selected],
    queryFn: () => getWorkspaceFile(selected!),
    enabled: !!selected,
  })
  const [draft, setDraft] = useState<string | null>(null)
  const [creating, setCreating] = useState<'file' | 'dir' | null>(null)
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

  // 新建流：commitCreate 统一处理文件/目录两种提交。

  // 新建流统一提交：file 建空文件（建完打开编辑），dir 建目录（父级自动创建）。
  function commitCreate() {
    const p = newName.trim()
    if (!p || !creating) return
    if (creating === 'file') {
      putWorkspaceFile(p, new Uint8Array())
        .then(() => {
          setNotice(`已创建 ${p}`)
          setNewName('')
          setSelected(p)
          setCreating(null)
          refresh()
        })
        .catch((e) => setNotice(`创建失败：${(e as Error).message}`))
    } else {
      mkdirWorkspace(p)
        .then(() => {
          setNotice(`已创建目录 ${p}`)
          setNewName('')
          setCreating(null)
          refresh()
        })
        .catch((e) => setNotice(`创建失败：${(e as Error).message}`))
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
    <div className="flex h-full min-h-0 flex-col bg-background">
      <PageHeader title="工作空间" subtitle="沙盒任务工件的宿主持久层" />

      <div className="flex flex-wrap items-center gap-3 bg-surface/50 px-6 py-3">
        <SearchInput
          className="w-72"
          value={taskQuery}
          onChange={setTaskQuery}
          placeholder="搜索路径（任务目录 / 文件名）…"
          ariaLabel="搜索工作区路径"
        />
        {allEntries.length > 0 && (
          <span className="text-xs text-text-secondary opacity-75">{taskCount} 个任务工作区</span>
        )}
        {notice && <span className="text-xs text-text-secondary">{notice}</span>}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* 文件树 + 新建工具栏 */}
        <div className="flex min-h-0 flex-col rounded-2xl border border-border bg-surface p-3">
          <div className="mb-3 flex items-center gap-2 border-b border-border pb-2">
            <button
              onClick={() => { setCreating('file'); setNewName('') }}
              title="新建文件"
              aria-label="新建文件"
              className={cn('text-text-secondary hover:text-text', creating === 'file' && 'text-accent')}
            >
              <FilePlus2 className="h-4 w-4" />
            </button>
            <button
              onClick={() => { setCreating('dir'); setNewName('') }}
              title="新建目录"
              aria-label="新建目录"
              className={cn('text-text-secondary hover:text-text', creating === 'dir' && 'text-accent')}
            >
              <FolderPlus className="h-4 w-4" />
            </button>
            <button
              onClick={collapseAll}
              title="折叠全部"
              aria-label="折叠全部"
              className="text-text-secondary hover:text-text"
            >
              <ChevronsDownUp className="h-4 w-4" />
            </button>
            <button onClick={() => refresh()} title="刷新" aria-label="刷新" className="text-text-secondary hover:text-text">
              <RefreshCw className="h-4 w-4" />
            </button>
          </div>
          {creating && (
            <div className="mb-3 flex items-center gap-1.5">
              <input
                autoFocus
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitCreate()
                  if (e.key === 'Escape') setCreating(null)
                }}
                placeholder={creating === 'file' ? '新文件路径，如 task/work/poc.py' : '新目录路径，如 task/work'}
                spellCheck={false}
                aria-label="新建路径"
                className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1 text-xs"
              />
              <button onClick={commitCreate} aria-label="确认新建" title="确认" className="text-accent hover:text-accent-hover">
                ✓
              </button>
              <button onClick={() => setCreating(null)} aria-label="取消新建" title="取消" className="text-text-secondary hover:text-text">
                <X className="h-4 w-4" />
              </button>
            </div>
          )}
          <FileTree
            entries={entries}
            selected={selected}
            collapsed={collapsed}
            onToggleDir={toggleDir}
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
            <p className="p-4 text-sm text-text-secondary">从左侧选择文件（行悬停可下载 / 删除）</p>
          ) : fileQuery.isLoading ? (
            <p className="p-4 text-sm text-text-secondary">加载中…</p>
          ) : fileQuery.data?.binary ? (
            <div className="space-y-2 p-2">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Badge size="sm" color="neutral">二进制</Badge>
                  <span className="text-sm text-text">{selected}</span>
                  <span className="text-xs text-text-secondary">{fileQuery.data.size} 字节（base64 预览禁用编辑）</span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => void download(selected)}
                    className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-text-secondary hover:text-text"
                  >
                    <Download className="h-3.5 w-3.5" /> 下载
                  </button>
                  <button
                    onClick={() => void remove(selected)}
                    className="flex items-center gap-1 text-xs text-text-secondary hover:text-text"
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
          ) : fileQuery.data?.truncated ? (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-sm text-text">{selected}</span>
                <div className="flex items-center gap-2">
                  {dirty && <Badge size="sm" color="neutral">未保存</Badge>}
                  <button
                    onClick={() => void download(selected)}
                    className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-text-secondary hover:text-text"
                  >
                    <Download className="h-3.5 w-3.5" /> 下载
                  </button>
                  <button
                    onClick={() => void save()}
                    disabled={!editing}
                    className={cn(
                      'rounded-lg border border-border px-2.5 py-1 text-xs',
                      editing ? 'text-text hover:bg-surface-2' : 'cursor-not-allowed text-text-secondary opacity-50',
                    )}
                  >
                    保存
                  </button>
                  <button
                    onClick={() => void remove(selected)}
                    className="flex items-center gap-1 text-xs text-text-secondary hover:text-text"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> 删除
                  </button>
                </div>
              </div>
              <p className="text-xs text-text-secondary">文件超过 4MiB，仅载入前段；保存将覆盖全文，请谨慎</p>
              <textarea
                aria-label="文件内容编辑器"
                value={draft ?? fileQuery.data?.content ?? ''}
                onChange={(e) => setDraft(e.target.value)}
                spellCheck={false}
                className="h-96 w-full rounded-lg border border-border bg-surface-2 p-3 font-mono text-xs leading-5"
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
                    className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-text-secondary hover:text-text"
                  >
                    <Download className="h-3.5 w-3.5" /> 下载
                  </button>
                  <button
                    onClick={() => void save()}
                    disabled={!editing}
                    className={cn(
                      'rounded-lg border border-border px-2.5 py-1 text-xs',
                      editing ? 'text-text hover:bg-surface-2' : 'cursor-not-allowed text-text-secondary opacity-50',
                    )}
                  >
                    保存
                  </button>
                  <button
                    onClick={() => void remove(selected)}
                    className="flex items-center gap-1 text-xs text-text-secondary hover:text-text"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> 删除
                  </button>
                </div>
              </div>
              <div
                aria-label="文件内容编辑器"
                className={cn(
                  'overflow-hidden rounded-lg border border-border bg-surface-2 text-xs',
                  '[&_.cm-editor]:min-h-[384px] [&_.cm-editor]:bg-surface-2 [&_.cm-gutters]:bg-surface-2 [&_.cm-gutters]:border-r-border',
                )}
              >
                <CodeMirror
                  value={draft ?? fileQuery.data?.content ?? ''}
                  onChange={(v) => setDraft(v)}
                  extensions={languageExtensions(selected)}
                  theme="dark"
                  height="384px"
                  basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: true }}
                />
              </div>
            </div>
          )}
        </div>
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
  collapsed,
  onToggleDir,
  onSelect,
  onDownload,
  onDelete,
}: {
  entries: WorkspaceEntry[]
  selected: string | null
  collapsed: Set<string>
  onToggleDir: (dirPath: string) => void
  onSelect: (path: string) => void
  onDownload: (path: string) => void
  onDelete: (path: string) => void
}) {
  return (
    <ul className="min-h-0 flex-1 space-y-0.5 overflow-y-auto text-sm">
      {entries.map((e) => {
        const depth = e.path.split('/').length - (e.is_dir ? 1 : 0)
        const base = e.path.split('/').pop() || e.path
        const isCollapsedDir = e.is_dir && collapsed.has(e.path)
        return (
          <li key={e.path} className="group/row">
            <div
              style={{ paddingLeft: `${depth * 14 + 8}px` }}
              className={cn(
                'flex items-center gap-1.5 rounded-lg py-1 text-left',
                e.is_dir && 'cursor-pointer hover:bg-surface-2',
                !e.is_dir && 'cursor-pointer hover:bg-surface-2',
                !e.is_dir && selected === e.path && 'bg-surface-2 text-text',
                e.is_dir && 'text-text-secondary',
              )}
              onClick={() => (e.is_dir ? onToggleDir(e.path) : onSelect(e.path))}
            >
              {e.is_dir ? (
                isCollapsedDir ? (
                  <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 opacity-60" />
                ) : (
                  <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 opacity-60" />
                )
              ) : (
                <span className="w-3.5 flex-shrink-0" />
              )}
              <span className="min-w-0 flex-1 truncate font-mono text-xs" title={e.path}>{base}</span>
              <span className="flex flex-shrink-0 items-center gap-1 opacity-0 transition-opacity group-focus-within/row:opacity-100 group-hover/row:opacity-100">
                <button
                  aria-label={`下载 ${e.path}`}
                  title={`下载${e.is_dir ? '（zip）' : ''}`}
                  onClick={(ev) => {
                    ev.stopPropagation()
                    onDownload(e.path)
                  }}
                  className="text-text-secondary hover:text-text"
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
                  className="text-text-secondary hover:text-text"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </span>
            </div>
          </li>
        )
      })}
      {entries.length === 0 && <li className="p-2 text-xs text-text-secondary">无匹配路径</li>}
    </ul>
  )
}
