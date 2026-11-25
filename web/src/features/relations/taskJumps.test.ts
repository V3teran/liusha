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
const ALL = ['task', 'conversation', 'findings', 'exploration', 'modelCall'] as const

describe('taskJumps', () => {
  it('从任务出发：落点按任务限定，顺序与侧栏一致，名字取侧栏导航', () => {
    const jumps = taskJumps(ctx(), ALL)
    expect(jumps.map((j) => [j.label, j.to])).toEqual([
      ['任务', '/tasks?task=t-1&selected=t-1'],
      ['对话', '/conversations/direct?conversation=c-1'],
      ['发现', '/findings?task=t-1'],
      ['探索图', '/exploration-graph?task=t-1'],
      ['模型调用', '/model-calls?task=t-1'],
    ])
  })

  it('从对话出发：任务、发现、探索图、模型调用都按这个对话限定', () => {
    const jumps = taskJumps(ctx({ from: 'conversation' }), ['task', 'findings', 'exploration', 'modelCall'])
    expect(jumps.map((j) => j.to)).toEqual([
      '/tasks?conversation=c-1&selected=t-1',
      '/findings?conversation=c-1',
      '/exploration-graph?conversation=c-1',
      '/model-calls?conversation=c-1',
    ])
  })

  // 纯聊天对话没有任务：依赖任务的去处都不成立（不做通向空页面的按钮），判定在规则里、调用方不必自己挡
  it('没有任务：任务、发现、探索图都没有，对话与模型调用照有', () => {
    const none = taskJumps(ctx({ taskId: '', findingCount: 2, from: 'conversation' }), ALL)
    expect(none.map((j) => j.to)).toEqual([
      '/conversations/direct?conversation=c-1',
      '/model-calls?conversation=c-1',
    ])
  })

  it('没有对话不放「对话」，没有发现不放「发现」', () => {
    expect(taskJumps(ctx({ conversationId: '', findingCount: 0 }), ALL).map((j) => j.target)).toEqual(['task', 'exploration', 'modelCall'])
  })

  it('代理捕获的任务，对话落在代理捕获标签页', () => {
    const [conv] = taskJumps(ctx({ source: 'proxy' }), ['conversation'])
    expect(conv.to).toBe('/conversations/proxy?conversation=c-1')
  })

  it('探索图可定位到一个节点（从发现出发：坐实它的结果），范围规则不变', () => {
    expect(taskJumps(ctx({ explorationNode: 'r-9' }), ['exploration'])[0].to).toBe('/exploration-graph?task=t-1&selected=r-9')
    expect(taskJumps(ctx({ from: 'conversation', explorationNode: 'r-9' }), ['exploration'])[0].to).toBe(
      '/exploration-graph?conversation=c-1&selected=r-9',
    )
  })

  it('只取要求的目标（详情不跳到自身）', () => {
    expect(taskJumps(ctx(), ['conversation', 'exploration']).map((j) => j.target)).toEqual(['conversation', 'exploration'])
  })
})
