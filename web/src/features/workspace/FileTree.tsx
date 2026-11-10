import { ChevronDown, ChevronRight, Download, Trash2 } from 'lucide-react'
import type { WorkspaceEntry } from '@/api/workspaces'
import { cn } from '@/lib/utils'
import { displayPath } from './displayPath'

/** 扁平文件树：服务端排序（顶层批次目录最新在前，组内目录前、路径序），
 * 深度即缩进（批次 → 任务 → work/out）。目录为弱化行，文件行可选中；行悬停露下载/删除。 */
export function FileTree({
  entries,
  selected,
  collapsed,
  nameMaps,
  onToggleDir,
  onSelect,
  onDownload,
  onDelete,
}: {
  entries: WorkspaceEntry[]
  selected: string | null
  collapsed: Set<string>
  nameMaps: { taskTitle: Record<string, string>; assignLabel: Record<string, string> }
  onToggleDir: (dirPath: string) => void
  onSelect: (path: string) => void
  onDownload: (path: string) => void
  onDelete: (path: string) => void
}) {
  /** 目录条目展示名：批次/任务层用人类可读名（批次标题 / 任务 brief，截 36 字），
   * 更深层用末段名——每个目录只展示自己的名字，不叠角色前缀。 */
  function dirDisplayName(e: WorkspaceEntry): string {
    const segs = e.path.replace(/\/$/, '').split('/')
    const trunc = (v: string) => (v.length > 36 ? v.slice(0, 36) + '…' : v)
    if (segs.length === 1) return trunc(nameMaps.assignLabel[segs[0]] ?? segs[0])
    if (segs.length === 2) return trunc(nameMaps.taskTitle[segs[1]] ?? segs[1])
    return segs[segs.length - 1]
  }

  /** 行悬停 title：与展示名同一套语义的全名（不截断、无 UUID 前缀）——
   * 原始路径对人无意义，删除确认框里仍有完整原始路径兜底。 */
  function rowTitle(e: WorkspaceEntry): string {
    const segs = e.path.replace(/\/$/, '').split('/')
    if (e.is_dir && segs.length === 1) return nameMaps.assignLabel[segs[0]] ?? segs[0]
    if (e.is_dir && segs.length === 2) return nameMaps.taskTitle[segs[1]] ?? segs[1]
    return displayPath(e.path)
  }

  return (
    <ul className="min-h-0 flex-1 space-y-0.5 overflow-y-auto text-sm">
      {entries.map((e) => {
        const depth = e.path.split('/').length - (e.is_dir ? 1 : 0)
        // 目录路径带尾斜杠：pop() 会取到空串回退成整条父链——必须先剥尾斜杠再取末段
        const base = e.path.replace(/\/$/, '').split('/').pop() || e.path
        const isCollapsedDir = e.is_dir && collapsed.has(e.path)
        return (
          <li key={e.path} className="group/row">
            <div
              style={{ paddingLeft: `${depth * 22 + 8}px` }}
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
              <span className="min-w-0 flex-1 truncate font-mono text-xs" title={rowTitle(e)}>
                {e.is_dir && e.path.replace(/\/$/, '').split('/').length <= 2
                  ? dirDisplayName(e)
                  : base}
              </span>
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
