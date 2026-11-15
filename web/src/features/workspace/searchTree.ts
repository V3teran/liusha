import type { WorkspaceEntry } from '@/api/workspaces'

/**
 * 树搜索：路径或显示名（批次标题、任务目标）包含关键词即命中。命中的目录连同其下全部内容
 * 一起保留（搜任务目标要看到这个任务的文件），命中项的祖先目录也保留（树要连得上）。
 */
export function searchTree(entries: WorkspaceEntry[], q: string): WorkspaceEntry[] {
  const hitDirs: string[] = []
  const keep = new Set<string>()
  for (const e of entries) {
    if (!(e.path.toLowerCase().includes(q) || (e.label ?? '').toLowerCase().includes(q))) continue
    keep.add(e.path)
    if (e.is_dir) hitDirs.push(e.path)
    const segs = e.path.replace(/\/$/, '').split('/')
    for (let i = 1; i < segs.length; i++) keep.add(segs.slice(0, i).join('/') + '/')
  }
  return entries.filter((e) => keep.has(e.path) || hitDirs.some((d) => e.path.startsWith(d)))
}
