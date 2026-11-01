/**
 * 工作空间域 API 客户端（GET/PUT/DELETE /workspaces）。
 *
 * 工作区 = 沙盒任务工件目录（work/out/profile）的宿主持久层；此处做
 * 文件级 CRUD。二进制文件以 base64 传输（binary 标记，前端禁用编辑）。
 */

import { del, get, getApiKey } from './client'

export interface WorkspaceTask {
  name: string
  mod_time: string
}

export interface WorkspaceEntry {
  path: string
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

export async function listWorkspaceTasks(): Promise<WorkspaceTask[]> {
  return (await get<{ tasks: WorkspaceTask[] }>('/workspaces')).tasks
}

export async function getWorkspaceTree(task: string): Promise<WorkspaceEntry[]> {
  return (await get<{ entries: WorkspaceEntry[] }>(`/workspaces/${encodeURIComponent(task)}/tree`)).entries
}

export async function getWorkspaceFile(task: string, path: string): Promise<WorkspaceFile> {
  return get(`/workspaces/${encodeURIComponent(task)}/content?path=${encodeURIComponent(path)}`)
}

/** 创建/覆盖文件（data 为原始字节；二进制安全）。 */
export async function putWorkspaceFile(task: string, path: string, data: Uint8Array): Promise<void> {
  const res = await fetch(`/api/workspaces/${encodeURIComponent(task)}/content?path=${encodeURIComponent(path)}`, {
    method: 'PUT',
    headers: { 'X-API-Key': getApiKey() },
    body: data as BodyInit,
  })
  if (!res.ok) throw new Error(`PUT ${path}: ${res.status}`)
}

/** 创建目录（父目录自动创建）。 */
export async function mkdirWorkspace(task: string, path: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${encodeURIComponent(task)}/content?path=${encodeURIComponent(path)}&dir=1`, {
    method: 'PUT',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`mkdir ${path}: ${res.status}`)
}

export async function deleteWorkspacePath(task: string, path: string): Promise<void> {
  await del(`/workspaces/${encodeURIComponent(task)}?path=${encodeURIComponent(path)}`)
}
