import { describe, expect, it } from 'vitest'
import { leadHasSpec, leadHint, leadMeta, nodeTitle, nodeChips, rejectionScope, stateOptions } from './kindTheme'
import type { LeadSpec, GraphNode } from '@/api/explorationgraph'

const SPEC: LeadSpec = { next: '用 admin 会话测其余模块', question: 'low 下是否同样可利用', potential: '每多一个模块多一条主张' }

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

  it('nodeChips：action 状态与加急上卡，无分类徽章、没有优先级', () => {
    const chips = nodeChips(
      node({ id: 'n-act', kind: 'action', state: 'running', urgent: true, content: { instruction: '列出入口' } }),
    )
    expect(chips.state?.label).toBe('执行中')
    expect(chips.category).toBeUndefined()
    expect(chips.urgent).toBe(true)
    expect(chips.priority).toBeUndefined()
    expect(chips.verification).toBeUndefined()
    expect(nodeChips(node({ id: 'n-act', kind: 'action', state: 'open', content: { instruction: '列出入口' } })).urgent).toBeUndefined()
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

  it('线索是事实观察的顶层字段 lead：带它的事实是线索；假设观察即使带了也不是', () => {
    expect(leadMeta(fact())).toBeNull()
    expect(leadMeta(fact({ lead: SPEC, verification_status: 'pending' }))).toBeNull()
    expect(leadMeta(fact({ lead: SPEC }))).toEqual({ closed: false, detail: '', spec: SPEC, kind: undefined })
    // 线索成为结构化字段之前落图的没有三样
    expect(leadMeta(fact({ lead: { next: '', question: '', potential: '' } }))).toEqual({
      closed: false,
      detail: '',
      spec: { next: '', question: '', potential: '' },
    })
  })

  it('关闭原因由类别与理由拼成；类别不在词表里按原值，缺理由只剩类别', () => {
    expect(leadMeta(fact({ lead: SPEC, lead_closed: { kind: 'dry', reason: '' } }))).toEqual({
      closed: true,
      detail: '干涸',
      spec: SPEC,
      kind: 'dry',
    })
    expect(leadMeta(fact({ lead: SPEC, lead_closed: { kind: 'out_of_scope', reason: ' 不在本期范围 ' } }))).toEqual({
      closed: true,
      detail: '超出任务目标：不在本期范围',
      spec: SPEC,
      kind: 'out_of_scope',
    })
    expect(leadMeta(fact({ lead: SPEC, lead_closed: { kind: 'odd', reason: '' } }))).toEqual({
      closed: true,
      detail: 'odd',
      spec: SPEC,
      kind: 'odd',
    })
  })

  it('悬停说明：有三样的逐行写，旧线索只有一句通用的说明', () => {
    expect(leadHint({ closed: false, detail: '', spec: SPEC })).toBe(
      [
        '线索：执行者已踩通、尚未推进到结论的路径',
        '下一步：用 admin 会话测其余模块',
        '要弄清：low 下是否同样可利用',
        '能换来：每多一个模块多一条主张',
      ].join('\n'),
    )
    const legacy = { next: '', question: '', potential: '' }
    expect(leadHint({ closed: false, detail: '', spec: legacy })).toBe('线索：执行者已踩通、尚未推进到结论的路径')
    expect(leadHasSpec({ ...legacy, question: 'q' })).toBe(true)
  })

  it('卡片的线索徽章：开着标「线索」，关闭后标「已关闭」', () => {
    expect(nodeChips(fact({ lead: SPEC })).lead).toEqual({ label: '线索', closed: false, detail: '', spec: SPEC, kind: undefined })
    expect(nodeChips(fact({ lead: SPEC, lead_closed: { kind: 'dry', reason: '' } })).lead).toEqual({
      label: '已关闭',
      closed: true,
      spec: SPEC,
      detail: '干涸',
      kind: 'dry',
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

// 状态筛选的选项只列图里实际有的状态。
describe('stateOptions', () => {
  it('只列有节点的状态，按生命周期的先后，带节点数', () => {
    expect(stateOptions({ completed: 10, running: 1, open: 1 }, 'all')).toEqual([
      { value: 'open', label: '待处理', count: 1 },
      { value: 'running', label: '执行中', count: 1 },
      { value: 'completed', label: '已完成', count: 10 },
    ])
    expect(stateOptions({}, 'all')).toEqual([])
  })

  it('当前选中的状态即使图上已经没有也留着，计数为 0', () => {
    expect(stateOptions({ completed: 3 }, 'failed')).toEqual([
      { value: 'completed', label: '已完成', count: 3 },
      { value: 'failed', label: '失败', count: 0 },
    ])
  })
})
