import { describe, expect, it } from 'vitest'
import { leadMeta, nodeTitle, nodeChips, rejectionScope } from './kindTheme'
import type { GraphNode } from '@/api/explorationgraph'

function node(o: Partial<GraphNode> = {}): GraphNode {
  return {
    id: 'n-obj',
    task_id: 't-1',
    kind: 'objective',
    content: {},
    source_type: 'user',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

describe('kindTheme（kind 视觉档案与内容提取）', () => {
  it('nodeTitle 按 kind 提取主文本', () => {
    expect(nodeTitle(node({ kind: 'objective', content: { description: '攻破 DVWA' } }))).toBe('攻破 DVWA')
    expect(nodeTitle(node({ id: 'n-act', kind: 'action', content: { instruction: '扫描登录页' } }))).toBe('扫描登录页')
    expect(nodeTitle(node({ id: 'n-obs', kind: 'observation', content: { statement: '发现弱口令' } }))).toBe('发现弱口令')
    expect(nodeTitle(node({ id: 'n-res', kind: 'result', content: { statement: 'SQLi 已坐实' } }))).toBe('SQLi 已坐实')
  })

  it('nodeTitle 主字段缺失或 content 非对象时返回空串', () => {
    expect(nodeTitle(node({ kind: 'objective', content: { title: '不是主字段' } }))).toBe('')
    expect(nodeTitle(node({ kind: 'objective', content: null }))).toBe('')
    expect(nodeTitle(node({ kind: 'action', content: '纯字符串载荷' }))).toBe('')
  })

  it('nodeChips：action 状态/P0-P1 优先级上卡，无分类徽章', () => {
    const chips = nodeChips(
      node({ id: 'n-act', kind: 'action', state: 'running', priority: 'high', content: { instruction: '列出入口' } }),
    )
    expect(chips.state?.label).toBe('执行中')
    expect(chips.category).toBeUndefined()
    expect(chips.priority?.code).toBe('P1')
    expect(chips.verification).toBeUndefined()
  })

  it('nodeChips：假设观察带分类/验证状态，优先级取节点层（P2 不上卡）', () => {
    const chips = nodeChips(
      node({
        id: 'n-obs',
        kind: 'observation',
        verification_status: 'refuted',
        priority: 'medium',
        content: { category: 'hypothesis' },
      }),
    )
    expect(chips.category).toBe('假设')
    expect(chips.verification).toBe('已证伪')
    expect(chips.priority).toBeUndefined()

    const high = nodeChips(node({ id: 'n-obs2', kind: 'observation', priority: 'high', content: { category: 'hypothesis' } }))
    expect(high.priority?.code).toBe('P1')
  })

  it('nodeChips：事实观察只有分类，没有验证状态', () => {
    const chips = nodeChips(node({ id: 'n-fact', kind: 'observation', content: { category: 'behavior' } }))
    expect(chips.category).toBe('行为')
    expect(chips.verification).toBeUndefined()
  })
})

describe('kindTheme（线索与否决范围）', () => {
  const fact = (o: Partial<GraphNode> = {}) =>
    node({ kind: 'observation', content: { statement: '上传目录可写' }, ...o })

  it('只有被标为线索的事实观察才是线索；假设观察即使带了标记也不是', () => {
    expect(leadMeta(fact())).toBeNull()
    expect(leadMeta(fact({ metadata: { opens_deeper: false } }))).toBeNull()
    expect(leadMeta(fact({ metadata: { opens_deeper: true }, verification_status: 'pending' }))).toBeNull()
    expect(leadMeta(fact({ metadata: { opens_deeper: true } }))).toEqual({ closed: false, detail: '' })
  })

  it('关闭原因由类别与理由拼成；类别不在词表里按原值，缺理由只剩类别', () => {
    expect(leadMeta(fact({ metadata: { opens_deeper: true, lead_closed: { kind: 'dry' } } }))).toEqual({
      closed: true,
      detail: '干涸',
    })
    expect(
      leadMeta(fact({ metadata: { opens_deeper: true, lead_closed: { kind: 'out_of_scope', reason: ' 不在本期范围 ' } } })),
    ).toEqual({ closed: true, detail: '超出任务目标：不在本期范围' })
    expect(leadMeta(fact({ metadata: { opens_deeper: true, lead_closed: { kind: 'odd' } } }))).toEqual({
      closed: true,
      detail: 'odd',
    })
  })

  it('卡片的线索徽章：开着标「线索」，关闭后标「已关闭」', () => {
    expect(nodeChips(fact({ metadata: { opens_deeper: true } })).lead).toEqual({ label: '线索', closed: false, detail: '' })
    expect(nodeChips(fact({ metadata: { opens_deeper: true, lead_closed: { kind: 'dry' } } })).lead).toEqual({
      label: '已关闭',
      closed: true,
      detail: '干涸',
    })
    expect(nodeChips(fact()).lead).toBeUndefined()
  })

  it('否决范围只在已否决的动作上有：没记范围按只否决这一步', () => {
    const action = (o: Partial<GraphNode> = {}) => node({ kind: 'action', state: 'rejected', ...o })
    expect(rejectionScope(action())).toBe('step')
    expect(rejectionScope(action({ metadata: { rejected_scope: 'lead' } }))).toBe('lead')
    expect(rejectionScope(action({ state: 'running' }))).toBeNull()
  })
})
