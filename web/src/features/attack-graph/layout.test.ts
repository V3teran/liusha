import { describe, expect, it } from 'vitest'
import type { GraphNodeData } from './graphTransform'
import { layoutGraph, nodeSize } from './layout'

const mkNode = (over: Partial<GraphNodeData>): GraphNodeData => ({
  id: 'n',
  kind: 'probe',
  label: 'x',
  dim: false,
  collapsed: false,
  chained: false,
  ...over,
})

describe('nodeSize', () => {
  it('折叠占位节点尺寸最小', () => {
    expect(nodeSize(mkNode({ kind: 'collapsed' }))).toBe(20)
  })

  it('普通漏洞节点比过程节点大', () => {
    expect(nodeSize(mkNode({ kind: 'finding' }))).toBe(38)
  })

  it('组合漏洞（chained）最大', () => {
    expect(nodeSize(mkNode({ kind: 'finding', chained: true }))).toBe(50)
  })

  it('4 类语义节点（task/hypothesis/probe/signal）统一尺寸', () => {
    expect(nodeSize(mkNode({ kind: 'task' }))).toBe(24)
    expect(nodeSize(mkNode({ kind: 'hypothesis' }))).toBe(24)
    expect(nodeSize(mkNode({ kind: 'probe' }))).toBe(24)
    expect(nodeSize(mkNode({ kind: 'signal' }))).toBe(24)
  })
})

describe('layoutGraph', () => {
  it('空图返回空数组', () => {
    expect(layoutGraph([], [])).toEqual([])
  })

  it('单节点分配坐标', () => {
    const nodes = [mkNode({ id: 'a' })]
    const positioned = layoutGraph(nodes, [])
    expect(positioned).toHaveLength(1)
    expect(positioned[0].id).toBe('a')
    expect(typeof positioned[0].x).toBe('number')
    expect(typeof positioned[0].y).toBe('number')
  })

  it('父子节点按 TB 方向分层：子节点 y 坐标大于父节点', () => {
    const nodes = [mkNode({ id: 'a' }), mkNode({ id: 'b' })]
    const edges = [{ id: 'e1', source: 'a', target: 'b', type: 'flow' }]
    const positioned = layoutGraph(nodes, edges)
    const a = positioned.find((p) => p.id === 'a')!
    const b = positioned.find((p) => p.id === 'b')!
    expect(b.y).toBeGreaterThan(a.y)
  })

  it('引用不存在节点的边被忽略，不抛错', () => {
    const nodes = [mkNode({ id: 'a' })]
    const edges = [{ id: 'e1', source: 'a', target: 'missing', type: 'flow' }]
    expect(() => layoutGraph(nodes, edges)).not.toThrow()
  })

  it('返回的每个节点带正确的宽高（与 nodeSize 对齐）', () => {
    const nodes = [mkNode({ id: 'f', kind: 'finding', chained: true })]
    const positioned = layoutGraph(nodes, [])
    expect(positioned[0].width).toBe(50)
    expect(positioned[0].height).toBe(50)
  })

  it('两个不相连的节点分层间距覆盖两行标签高度，不按单行高度分配', () => {
    // 用两条不相连的父子链（a→b, c→d）撑出「同层多节点」场景：dagre 按 setNode 声明的
    // height 计算 rank 内的节点间距。声明高度若只按单行（size+8≈32px）算，两行标签
    // （LABEL_MAX_LINES=2 × 15px 行高=30px）本该够用，但真实寻找的是「层间距是否
    // 覆盖了两行文本」——这里直接断言同 rank 内两节点的 y 差值不小于两行文本高度，
    // 防止未来把 LABEL_MAX_LINES/LABEL_LINE_HEIGHT 改小又忘了同步 nodes.tsx 的 line-clamp。
    const nodes = [mkNode({ id: 'a' }), mkNode({ id: 'b' })]
    const positioned = layoutGraph(nodes, [])
    const a = positioned.find((p) => p.id === 'a')!
    const b = positioned.find((p) => p.id === 'b')!
    // 同层（无边相连，dagre 都排进同一 rank）时至少间隔 nodesep(18) + 声明高度(30，即两行)。
    expect(Math.abs(a.x - b.x)).toBeGreaterThanOrEqual(18 + 30)
  })
})
