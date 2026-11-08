/**
 * 工作空间域 API 客户端（/workspaces）。
 *
 * 工作区 = 各任务在沙箱内产生与使用的文件（顶层目录即任务）。路径全部相对
 * 工作区根；二进制文件以 base64 传输（binary 标记，前端禁用编辑）；
 * 下载走 raw 通道（文件原样 / 目录 zip），同源 fetch 自动携带会话 Cookie。
 */

import { del, get } from './client'

export interface WorkspaceEntry {
  path: string
  depth: number
  size: number
  is_dir: boolean
  mod_time: string
}

export interface WorkspaceFile {
  path: string
  binary: boolean
  content: string
  size: number
  truncated: boolean
  mod_time: string
}

export async function getWorkspaceTree(): Promise<WorkspaceEntry[]> {
  return (await get<{ entries: WorkspaceEntry[] }>('/workspaces/tree')).entries
}

export async function getWorkspaceFile(path: string): Promise<WorkspaceFile> {
  return get(`/workspaces/content?path=${encodeURIComponent(path)}`)
}

/** 创建/覆盖文件（data 为原始字节；二进制安全）。 */
export async function putWorkspaceFile(path: string, data: Uint8Array): Promise<void> {
  const res = await fetch(`/api/workspaces/content?path=${encodeURIComponent(path)}`, {
    method: 'PUT',
    body: data as BodyInit,
  })
  if (!res.ok) throw new Error(`PUT ${path}: ${res.status}`)
}

/** 创建目录（父目录自动创建）。 */
export async function mkdirWorkspace(path: string): Promise<void> {
  const res = await fetch(`/api/workspaces/content?path=${encodeURIComponent(path)}&dir=1`, {
    method: 'PUT',
  })
  if (!res.ok) throw new Error(`mkdir ${path}: ${res.status}`)
}

export async function deleteWorkspacePath(path: string): Promise<void> {
  await del(`/workspaces?path=${encodeURIComponent(path)}`)
}

/** 下载（文件原样；目录 zip）。同源 fetch 自动携带会话 Cookie。 */
export async function downloadWorkspacePath(path: string): Promise<void> {
  const res = await fetch(`/api/workspaces/raw?path=${encodeURIComponent(path)}`)
  if (!res.ok) throw new Error(`下载 ${path}: ${res.status}`)
  const blob = await res.blob()
  const base = path.split('/').pop() || path
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = base
  a.click()
  URL.revokeObjectURL(url)
}
