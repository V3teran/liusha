import { describe, expect, it } from 'vitest'
import type { AttackGraph, AttackGraphNode } from '@/api/types'
import { nodeLabel, toGraphData } from './graphTransform'

// mkNode 补齐世界模型节点必填字段，测试只覆写关心的部分。
function mkNode(over: Partial<AttackGraphNode>): AttackGraphNode {
  return {
    id: 'n',
    seq: 1,
    kind: 'asset',
    ref: { domain: 'web', ref_kind: 'endpoint', locator: '/x' },
    attrs: {},
    confidence: 'confirmed',
    ...over,
  }
}

function mkGraph(over: Partial<AttackGraph>): AttackGraph {
  return { task_id: 't', scan_id: 's', nodes: [], edges: [], verifications: [], ...over }
}

describe('nodeLabel', () => {
  it('finding 用 attrs.summary', () => {
    expect(nodeLabel(mkNode({ kind: 'finding', attrs: { summary: 'SQL 注入' } }))).toBe('SQL 注入')
  })

  it('finding 无 summary 兜底到 ref.locator', () => {
    expect(nodeLabel(mkNode({ kind: 'finding', attrs: {}, ref: { domain: 'web', ref_kind: 'endpoint', locator: '/login' } }))).toBe('/login')
  })

  it('非 finding 用 ref.locator', () => {
    expect(nodeLabel(mkNode({ kind: 'asset', ref: { domain: 'web', ref_kind: 'endpoint', locator: '/api/users' } }))).toBe('/api/users')
  })

  it('非 finding 无 locator 兜底到 attrs.service 再到 kind', () => {
    expect(nodeLabel(mkNode({ kind: 'asset', ref: { domain: 'web', ref_kind: 'endpoint', locator: '' }, attrs: { service: 'nginx' } }))).toBe('nginx')
    expect(nodeLabel(mkNode({ kind: 'credential', ref: { domain: 'web', ref_kind: 'x', locator: '' }, attrs: {} }))).toBe('credential')
  })
})

describe('toGraphData', () => {
  it('null 图返回空节点边', () => {
    expect(toGraphData(null)).toEqual({ nodes: [], edges: [] })
  })

  it('全节点全边直出（无折叠/降权启发式）', () => {
    const g = mkGraph({
      nodes: [
        mkNode({ id: 'a', kind: 'asset', seq: 1 }),
        mkNode({ id: 'f', kind: 'finding', seq: 2, attrs: { summary: '洞', severity: 'high' }, confidence: 'confirmed' }),
      ],
      edges: [{ id: 'e1', rel: 'on', source: 'f', target: 'a', attrs: {} }],
    })
    const { nodes, edges } = toGraphData(g)
    expect(nodes).toHaveLength(2)
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: 'f', target: 'a', rel: 'on' })
  })

  it('finding 节点透传 severity，非 finding 不带 severity', () => {
    const g = mkGraph({
      nodes: [
        mkNode({ id: 'f', kind: 'finding', attrs: { severity: 'critical' } }),
        mkNode({ id: 'a', kind: 'asset', attrs: { severity: 'ignored' } }),
      ],
    })
    const { nodes } = toGraphData(g)
    expect(nodes.find((n) => n.id === 'f')?.severity).toBe('critical')
    expect(nodes.find((n) => n.id === 'a')?.severity).toBeUndefined()
  })

  it('assumed 节点透传 confidence（供渲染层虚线描边）', () => {
    const g = mkGraph({ nodes: [mkNode({ id: 'a', confidence: 'assumed' })] })
    expect(toGraphData(g).nodes[0].confidence).toBe('assumed')
  })

  it('自环边被跳过', () => {
    const g = mkGraph({
      nodes: [mkNode({ id: 'a' })],
      edges: [{ id: 'e', rel: 'enables', source: 'a', target: 'a', attrs: {} }],
    })
    expect(toGraphData(g).edges).toHaveLength(0)
  })

  it('同源同标同关系的重复边只保留一条', () => {
    const g = mkGraph({
      nodes: [mkNode({ id: 'a' }), mkNode({ id: 'b' })],
      edges: [
        { id: 'e1', rel: 'enables', source: 'a', target: 'b', attrs: {} },
        { id: 'e2', rel: 'enables', source: 'a', target: 'b', attrs: {} },
      ],
    })
    expect(toGraphData(g).edges).toHaveLength(1)
  })

  it('同两端但不同关系的边都保留', () => {
    const g = mkGraph({
      nodes: [mkNode({ id: 'a' }), mkNode({ id: 'b' })],
      edges: [
        { id: 'e1', rel: 'enables', source: 'a', target: 'b', attrs: {} },
        { id: 'e2', rel: 'derives', source: 'a', target: 'b', attrs: {} },
      ],
    })
    expect(toGraphData(g).edges).toHaveLength(2)
  })

  it('缺 id 的边用序号兜底出稳定 id', () => {
    const g = mkGraph({
      nodes: [mkNode({ id: 'a' }), mkNode({ id: 'b' })],
      edges: [{ id: '', rel: 'on', source: 'a', target: 'b', attrs: {} }],
    })
    expect(toGraphData(g).edges[0].id).toBe('e0')
  })
})
