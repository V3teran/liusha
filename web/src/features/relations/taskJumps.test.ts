import { describe, expect, it } from 'vitest'
import { taskJumps, type TaskJumpContext } from './taskJumps'

const ctx = (over: Partial<TaskJumpContext> = {}): TaskJumpContext => ({
  taskId: 't-1',
  conversationId: 'c-1',
  source: 'direct',
  findingCount: 2,
  from: 'task',
  ...over,
})
const ALL = ['task', 'conversation', 'findings', 'exploration'] as const

describe('taskJumps', () => {
  it('从任务出发：落点按任务限定，顺序与侧栏一致，名字取侧栏导航', () => {
    const jumps = taskJumps(ctx(), ALL)
    expect(jumps.map((j) => [j.label, j.to])).toEqual([
      ['任务', '/tasks?task=t-1&selected=t-1'],
      ['对话', '/conversations/direct?conversation=c-1'],
      ['漏洞', '/findings?task=t-1'],
      ['探索图', '/exploration-graph?task=t-1'],
    ])
  })

  it('从对话出发：任务、漏洞、探索图都按这个对话限定', () => {
    const jumps = taskJumps(ctx({ from: 'conversation' }), ['task', 'findings', 'exploration'])
    expect(jumps.map((j) => j.to)).toEqual([
      '/tasks?conversation=c-1&selected=t-1',
      '/findings?conversation=c-1',
      '/exploration-graph?conversation=c-1',
    ])
  })

  it('没有对话不放「对话」，没有漏洞不放「漏洞」', () => {
    expect(taskJumps(ctx({ conversationId: '', findingCount: 0 }), ALL).map((j) => j.target)).toEqual(['task', 'exploration'])
  })

  it('代理捕获的任务，对话落在代理捕获标签页', () => {
    const [conv] = taskJumps(ctx({ source: 'proxy' }), ['conversation'])
    expect(conv.to).toBe('/conversations/proxy?conversation=c-1')
  })

  it('探索图可定位到一个节点（从漏洞出发：坐实它的结果），范围规则不变', () => {
    expect(taskJumps(ctx({ explorationNode: 'r-9' }), ['exploration'])[0].to).toBe('/exploration-graph?task=t-1&selected=r-9')
    expect(taskJumps(ctx({ from: 'conversation', explorationNode: 'r-9' }), ['exploration'])[0].to).toBe(
      '/exploration-graph?conversation=c-1&selected=r-9',
    )
  })

  it('只取要求的目标（详情不跳到自身）', () => {
    expect(taskJumps(ctx(), ['conversation', 'exploration']).map((j) => j.target)).toEqual(['conversation', 'exploration'])
  })
})
