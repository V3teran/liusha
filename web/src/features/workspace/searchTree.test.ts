import { describe, expect, it } from 'vitest'
import type { WorkspaceEntry } from '@/api/workspaces'
import { searchTree } from './searchTree'

const e = (path: string, label?: string): WorkspaceEntry => ({
  path,
  depth: path.replace(/\/$/, '').split('/').length - 1,
  size: 0,
  is_dir: path.endsWith('/'),
  mod_time: '',
  label,
})

const TREE = [
  e('a1/', '夜间复扫'),
  e('a1/t1/', '测试商城 shop.local'),
  e('a1/t1/out/'),
  e('a1/t1/out/report.md'),
  e('a1/t2/', '测试博客 blog.local'),
  e('a1/t2/notes.txt'),
]

describe('searchTree', () => {
  it('按显示名命中任务目录时，保留它的全部内容与祖先目录', () => {
    expect(searchTree(TREE, '商城').map((x) => x.path)).toEqual(['a1/', 'a1/t1/', 'a1/t1/out/', 'a1/t1/out/report.md'])
  })

  it('按文件名命中时只保留文件与祖先', () => {
    expect(searchTree(TREE, 'notes').map((x) => x.path)).toEqual(['a1/', 'a1/t2/', 'a1/t2/notes.txt'])
  })

  it('大小写不敏感（调用方传入小写关键词）', () => {
    expect(searchTree(TREE, 'blog.local').map((x) => x.path)).toContain('a1/t2/notes.txt')
  })
})
