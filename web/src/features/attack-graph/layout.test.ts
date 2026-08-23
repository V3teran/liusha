import { describe, expect, it } from 'vitest'
import type { GraphNodeData } from './graphTransform'
import { layoutGraph, nodeSize } from './layout'

const mkNode = (over: Partial<GraphNodeData>): GraphNodeData => ({
  id: 'n',
  seq: 1,
  kind: 'asset',
  label: 'x',
  confidence: 'confirmed',
  ...over,
})

describe('nodeSize', () => {
  it('漏洞星形最大（攻击链终点视觉锚）', () => {
    expect(nodeSize(mkNode({ kind: 'finding' }))).toBe(44)
  })

  it('target（交战根）次之', () => {
    expect(nodeSize(mkNode({ kind: 'target' }))).toBe(36)
  })

  it('asset/credential/access 统一尺寸', () => {
    expect(nodeSize(mkNode({ kind: 'asset' }))).toBe(26)
    expect(nodeSize(mkNode({ kind: 'credential' }))).toBe(26)
    expect(nodeSize(mkNode({ kind: 'access' }))).toBe(26)
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
    const edges = [{ id: 'e1', source: 'a', target: 'b', rel: 'enables' }]
    const positioned = layoutGraph(nodes, edges)
    const a = positioned.find((p) => p.id === 'a')!
    const b = positioned.find((p) => p.id === 'b')!
    expect(b.y).toBeGreaterThan(a.y)
  })

  it('引用不存在节点的边被忽略，不抛错', () => {
    const nodes = [mkNode({ id: 'a' })]
    const edges = [{ id: 'e1', source: 'a', target: 'missing', rel: 'enables' }]
    expect(() => layoutGraph(nodes, edges)).not.toThrow()
  })

  it('返回的每个节点带正确的宽高（与 nodeSize 对齐）', () => {
    const nodes = [mkNode({ id: 'f', kind: 'finding' })]
    const positioned = layoutGraph(nodes, [])
    expect(positioned[0].width).toBe(44)
    expect(positioned[0].height).toBe(44)
  })

  it('同层多节点按声明宽度 + nodesep 拉开，不重叠', () => {
    // 同层（无边相连，dagre 排进同一 rank）水平间距 = 声明宽度(size+140) + nodesep(18)。
    const nodes = [mkNode({ id: 'a' }), mkNode({ id: 'b' })]
    const positioned = layoutGraph(nodes, [])
    const a = positioned.find((p) => p.id === 'a')!
    const b = positioned.find((p) => p.id === 'b')!
    expect(Math.abs(a.x - b.x)).toBeGreaterThanOrEqual(18)
  })
})
