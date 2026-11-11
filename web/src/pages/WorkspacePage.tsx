import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { lazy, Suspense, useMemo, useRef, useState } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronsDownUp, Download, FilePlus2, FolderPlus, RefreshCw, Trash2, X } from 'lucide-react'
import {
  deleteWorkspacePath,
  downloadWorkspacePath,
  getWorkspaceFile,
  getWorkspaceTree,
  mkdirWorkspace,
  putWorkspaceFile,
} from '@/api/workspaces'
import { listTasks } from '@/api/tasks'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { controlClass } from '@/components/ui/control'
import { cn } from '@/lib/utils'
import { FilterBar } from '@/components/FilterBar'
import { FileTree } from '@/features/workspace/FileTree'
import { displayPath } from '@/features/workspace/displayPath'

// 文件树工具栏的方形图标按钮（28px 热区，图标居中）。
const TOOL_ICON = 'w-7 px-0'

// 编辑器（CodeMirror 核心 + 语言包）独立分包：文件树与列表先渲染，选中文件后才下载编辑器。
const FileEditor = lazy(() =>
  import('@/features/workspace/FileEditor').then((m) => ({ default: m.FileEditor })),
)

/**
 * 工作空间页：各任务在沙箱内的文件（work/out/profile）CRUD。
 * 直接展示整棵工作区文件结构（两级分层：顶层为批次 assignment 分组，其下
 * 任务 task 子目录，最新在前），搜索框过滤路径；
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

  // 友好命名映射：目录树里的批次/任务目录原本是 UUID（又长又不可读），
  // 用任务列表把 ID 翻译成人类可读的 brief（截断展示，悬停 title 仍显原始路径）。
  const tasksQuery = useQuery({
    queryKey: ['tasks', 'for-workspace'],
    queryFn: () => listTasks(1, 0), // 全量分页页（size=0 = 不限），建 UUID→brief 映射
  })
  const nameMaps = useMemo(() => {
    const taskTitle: Record<string, string> = {}
    const assignLabel: Record<string, string> = {}
    for (const t of tasksQuery.data?.items ?? []) {
      taskTitle[t.id] = t.brief
      if (!(t.assignment_id in assignLabel)) assignLabel[t.assignment_id] = t.brief
    }
    return { taskTitle, assignLabel }
  }, [tasksQuery.data])

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
  const [saving, setSaving] = useState(false)
  // 当前选中文件的同步镜像：保存是异步的，回调里要拿「此刻」选中的是谁，闭包里的是发起时的旧值。
  const selectedRef = useRef<string | null>(null)
  selectedRef.current = selected
  const [creating, setCreating] = useState<'file' | 'dir' | null>(null)
  const [newName, setNewName] = useState('')
  const [notice, setNotice] = useState('')

  const dirty = fileQuery.data ? draft !== null && draft !== fileQuery.data.content : false
  const editing = draft !== null

  function refresh() {
    void qc.invalidateQueries({ queryKey: ['workspace'] })
  }

  // 保存：捕获发起时的文件与草稿快照。请求期间用户可能切到别的文件或继续输入——
  // 成功后只在「仍是同一文件、草稿未再变动」时清草稿，否则保留新编辑；失败保留草稿供重试。
  async function save() {
    if (!selected || draft === null || saving) return
    const path = selected
    const snapshot = draft
    setSaving(true)
    try {
      await putWorkspaceFile(path, new TextEncoder().encode(snapshot))
      if (selectedRef.current === path) {
        setDraft((cur) => (cur === snapshot ? null : cur))
      }
      setNotice(`已保存 ${displayPath(path)}`)
      refresh()
    } catch (e) {
      setNotice(`保存失败：${displayPath(path)}（${(e as Error).message}），草稿已保留`)
    } finally {
      setSaving(false)
    }
  }

  // 新建流：commitCreate 统一处理文件/目录两种提交。

  // 新建流统一提交：file 建空文件（建完打开编辑），dir 建目录（父级自动创建）。
  function commitCreate() {
    const p = newName.trim()
    if (!p || !creating) return
    if (creating === 'file') {
      putWorkspaceFile(p, new Uint8Array())
        .then(() => {
          setNotice(`已创建 ${displayPath(p)}`)
          setNewName('')
          setSelected(p)
          setCreating(null)
          refresh()
        })
        .catch((e) => setNotice(`创建失败：${displayPath(p)}（${(e as Error).message}）`))
    } else {
      mkdirWorkspace(p)
        .then(() => {
          setNotice(`已创建目录 ${displayPath(p)}`)
          setNewName('')
          setCreating(null)
          refresh()
        })
        .catch((e) => setNotice(`创建失败：${displayPath(p)}（${(e as Error).message}）`))
    }
  }

  async function download(path: string) {
    try {
      await downloadWorkspacePath(path)
      setNotice(`已下载 ${displayPath(path)}`)
    } catch (e) {
      setNotice(`下载失败：${displayPath(path)}（${(e as Error).message}）`)
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
      setNotice(`已删除 ${displayPath(path)}`)
      refresh()
    } catch (e) {
      setNotice(`删除失败：${displayPath(path)}（${(e as Error).message}）`)
    }
  }

  const assignmentCount = allEntries.filter((e) => e.depth === 0 && e.is_dir).length

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="工作空间" subtitle="浏览、编辑各任务在沙箱内产生与使用的文件" />

      <FilterBar
        label="工作区筛选"
        trailing={
          allEntries.length > 0 ? (
            <span className="whitespace-nowrap text-sm text-text-secondary">
              <strong className="font-semibold tabular-nums text-text">{assignmentCount}</strong> 个批次工作区
            </span>
          ) : undefined
        }
      >
        <SearchInput
          className="w-72"
          value={taskQuery}
          onChange={setTaskQuery}
          placeholder="搜索路径（批次 / 任务 / 文件名）…"
          ariaLabel="搜索工作区路径"
        />
        {notice && (
          <span role="status" className="text-xs text-text-secondary">
            {notice}
          </span>
        )}
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* 文件树 + 新建工具栏 */}
        <div className="flex min-h-0 flex-col rounded-xl border border-border bg-surface p-3 shadow-sm">
          <div className="mb-3 flex items-center gap-1 border-b border-border pb-2">
            <Button
              variant="ghost"
              size="xs"
              className={cn(TOOL_ICON, creating === 'file' && 'bg-surface-2 text-text')}
              onClick={() => { setCreating('file'); setNewName('') }}
              title="新建文件"
              aria-label="新建文件"
              aria-pressed={creating === 'file'}
            >
              <FilePlus2 />
            </Button>
            <Button
              variant="ghost"
              size="xs"
              className={cn(TOOL_ICON, creating === 'dir' && 'bg-surface-2 text-text')}
              onClick={() => { setCreating('dir'); setNewName('') }}
              title="新建目录"
              aria-label="新建目录"
              aria-pressed={creating === 'dir'}
            >
              <FolderPlus />
            </Button>
            <Button variant="ghost" size="xs" className={TOOL_ICON} onClick={collapseAll} title="折叠全部" aria-label="折叠全部">
              <ChevronsDownUp />
            </Button>
            <Button variant="ghost" size="xs" className={TOOL_ICON} onClick={() => refresh()} title="刷新" aria-label="刷新">
              <RefreshCw />
            </Button>
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
                className={controlClass('sm', 'min-w-0 flex-1 font-mono')}
              />
              <Button size="sm" onClick={commitCreate} aria-label="确认新建" title="确认">
                <Check />
              </Button>
              <Button variant="ghost" size="sm" className="w-8 px-0" onClick={() => setCreating(null)} aria-label="取消新建" title="取消">
                <X />
              </Button>
            </div>
          )}
          <FileTree
            entries={entries}
            selected={selected}
            collapsed={collapsed}
            nameMaps={nameMaps}
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
        <div className="rounded-xl border border-border bg-surface p-3 shadow-sm">
          {!selected ? (
            <p className="p-4 text-sm text-text-secondary">从左侧选择文件（行悬停可下载 / 删除）</p>
          ) : fileQuery.isLoading ? (
            <p className="p-4 text-sm text-text-secondary">加载中…</p>
          ) : fileQuery.data?.binary ? (
            <div className="space-y-2 p-2">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Badge size="sm" color="neutral">二进制</Badge>
                  <span className="text-sm text-text" title={displayPath(selected)}>{displayPath(selected)}</span>
                  <span className="text-xs text-text-secondary">{fileQuery.data.size} 字节（base64 预览禁用编辑）</span>
                </div>
                <div className="flex items-center gap-2">
                  <Button variant="secondary" size="xs" onClick={() => void download(selected)}>
                    <Download /> 下载
                  </Button>
                  <Button variant="danger" size="xs" onClick={() => void remove(selected)}>
                    <Trash2 /> 删除
                  </Button>
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
                <span className="truncate text-sm text-text" title={displayPath(selected)}>{displayPath(selected)}</span>
                <div className="flex items-center gap-2">
                  <Button variant="secondary" size="xs" onClick={() => void download(selected)}>
                    <Download /> 下载
                  </Button>
                  <Button variant="danger" size="xs" onClick={() => void remove(selected)}>
                    <Trash2 /> 删除
                  </Button>
                </div>
              </div>
              <p className="text-xs text-text-secondary">文件超过 4MiB，仅载入前段作只读预览——在此保存会用前段覆盖全文、丢失尾部，如需修改请下载后编辑</p>
              <textarea
                aria-label="文件内容预览（只读）"
                value={fileQuery.data?.content ?? ''}
                readOnly
                spellCheck={false}
                className="h-96 w-full rounded-lg border border-border bg-surface-2 p-3 font-mono text-xs leading-5"
              />
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-sm text-text" title={displayPath(selected)}>{displayPath(selected)}</span>
                <div className="flex items-center gap-2">
                  {dirty && <Badge size="sm" color="neutral">未保存</Badge>}
                  <Button variant="secondary" size="xs" onClick={() => void download(selected)}>
                    <Download /> 下载
                  </Button>
                  <Button size="xs" onClick={() => void save()} disabled={!editing || saving}>
                    {saving ? '保存中…' : '保存'}
                  </Button>
                  <Button variant="danger" size="xs" onClick={() => void remove(selected)}>
                    <Trash2 /> 删除
                  </Button>
                </div>
              </div>
              <div
                aria-label="文件内容编辑器"
                className={cn(
                  'overflow-hidden rounded-lg border border-border bg-surface-2 text-xs',
                  '[&_.cm-editor]:min-h-[384px] [&_.cm-editor]:bg-surface-2 [&_.cm-gutters]:bg-surface-2 [&_.cm-gutters]:border-r-border',
                )}
              >
                <Suspense fallback={<p className="p-3 text-text-secondary">编辑器加载中…</p>}>
                  <FileEditor
                    path={selected}
                    value={draft ?? fileQuery.data?.content ?? ''}
                    onChange={setDraft}
                  />
                </Suspense>
              </div>
            </div>
          )}
        </div>
      </div>
      </div>
    </div>
  )
}
