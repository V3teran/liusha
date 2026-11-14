import { describe, expect, it } from 'vitest'
import { buildFlowGraph, applyFocus } from './layout'
import type { ConversationGraph, GraphNode } from '@/api/explorationgraph'

function node(o: Partial<GraphNode> = {}): GraphNode {
  return {
    id: 'n-obj',
    task_id: 't-1',
    kind: 'objective',
    content: { description: '攻破 DVWA' },
    source_type: 'user',
    created_at: '2026-06-10T00:00:00Z',
    updated_at: '2026-06-10T00:00:00Z',
    ...o,
  }
}

const GRAPH: ConversationGraph = {
  nodes: [
    node(),
    node({ id: 'n-act-a', kind: 'action', state: 'running', content: { instruction: '端口发现' } }),
    node({ id: 'n-act-b', kind: 'action', state: 'open', content: { instruction: '目录扫描' } }),
    node({ id: 'n-obs', kind: 'observation', verification_status: 'confirmed', content: { statement: '发现登录页' } }),
    node({ id: 'n-res', kind: 'result', content: { summary: '弱口令坐实' } }),
  ],
  edges: [
    { src_id: 'n-obj', dst_id: 'n-act-a', rel: 'generates', created_at: '2026-06-10T00:00:00Z' },
    { src_id: 'n-obj', dst_id: 'n-act-b', rel: 'generates', created_at: '2026-06-10T00:00:00Z' },
    { src_id: 'n-act-a', dst_id: 'n-obs', rel: 'generates', created_at: '2026-06-10T00:00:00Z' },
    { src_id: 'n-obs', dst_id: 'n-res', rel: 'generates', created_at: '2026-06-10T00:00:00Z' },
    { src_id: 'n-res', dst_id: 'n-obj', rel: 'triggers', created_at: '2026-06-10T00:00:00Z' },
  ],
}

describe('layout（LR 布局 + 边关系 + 聚焦高亮）', () => {
  it('buildFlowGraph：LR 方向、三种边型、running 出边流动、统计全量', () => {
    const { nodes, edges, stats } = buildFlowGraph(GRAPH, 'all', 'all', new Map())

    expect(stats).toEqual({
      total: 5,
      edgeCount: 5,
      byKind: { objective: 1, action: 2, observation: 1, result: 1 },
    })
    expect(nodes).toHaveLength(5)
    // 节点声明显式尺寸：MiniMap 渲染的前提，也是 dagre 布局的尺寸假设
    expect(nodes[0].width).toBe(264)
    expect(nodes[0].height).toBe(104)
    // LR：action 排在 objective 右侧，observation 更右
    const pos = Object.fromEntries(nodes.map((n) => [n.id, n.position]))
    expect(pos['n-act-a'].x).toBeGreaterThan(pos['n-obj'].x)
    expect(pos['n-obs'].x).toBeGreaterThan(pos['n-act-a'].x)

    // generates 实线（无虚线）
    const gens = edges.find((e) => e.id.startsWith('n-obj-n-act-a'))
    expect(gens?.style?.strokeDasharray).toBeUndefined()
    // triggers 琥珀虚线
    const trig = edges.find((e) => e.source === 'n-res')
    expect(trig?.style?.strokeDasharray).toBe('6 4')
    // running action 的出边带流动动画
    expect(edges.find((e) => e.source === 'n-act-a')?.animated).toBe(true)
    expect(edges.find((e) => e.source === 'n-obj')?.animated).not.toBe(true)
  })

  it('buildFlowGraph：kind 筛选后剔除悬空边', () => {
    const { nodes, edges, stats } = buildFlowGraph(GRAPH, 'action', 'all', new Map())
    expect(nodes.map((n) => n.id).sort()).toEqual(['n-act-a', 'n-act-b'])
    // generates 边的另一端（objective）被筛掉 → 剔除
    expect(edges).toEqual([])
    expect(stats.byKind.action).toBe(2) // 统计保持全量
  })

  it('applyFocus：选中节点高亮、直连邻居常亮、其余降暗；取消后恢复', () => {
    const flow = buildFlowGraph(GRAPH, 'all', 'all', new Map())
    const view = applyFocus(flow, 'n-act-b')

    const cls = Object.fromEntries(view.nodes.map((n) => [n.id, n.className]))
    expect(cls['n-act-b']).toBe('exp-selected')
    expect(cls['n-act-a']).toBe('exp-dimmed') // 与选中节点无直连边
    expect(cls['n-obj']).toBeUndefined() // generates 直连邻居
    expect(cls['n-obs']).toBe('exp-dimmed')

    const trig = view.edges.find((e) => e.source === 'n-res')
    expect(trig?.style?.stroke).toMatch(/22$/) // 非相关边淡出（透明色后缀）

    const cleared = applyFocus(flow, '')
    expect(cleared.nodes.every((n) => !n.className)).toBe(true)
  })
})
