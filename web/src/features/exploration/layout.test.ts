import { describe, expect, it } from 'vitest'
import { applyFocus, buildFlowGraph, EDGE_LOOKS, EDGE_LOOK_LABELS } from './layout'
import type { ExplorationGraph, GraphNode } from '@/api/explorationgraph'

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

const GRAPH: ExplorationGraph = {
  nodes: [
    node(),
    node({ id: 'n-act-a', kind: 'action', state: 'running', content: { instruction: '端口发现' } }),
    node({ id: 'n-act-b', kind: 'action', state: 'open', content: { instruction: '目录扫描' } }),
    node({ id: 'n-obs', kind: 'observation', verification_status: 'confirmed', content: { statement: '发现登录页' } }),
    node({ id: 'n-res', kind: 'result', content: { statement: '弱口令坐实' } }),
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
  it('buildFlowGraph：LR 方向、running 出边流动、统计全量', () => {
    const { nodes, edges, stats } = buildFlowGraph(GRAPH, 'all', 'all', new Map())

    expect(stats).toEqual({
      total: 5,
      edgeCount: 5,
      byKind: { objective: 1, action: 2, observation: 1, result: 1 },
      directionEdgeCount: 1,
    })
    expect(nodes).toHaveLength(5)
    // 节点声明显式尺寸：MiniMap 渲染的前提，也是 dagre 布局的尺寸假设
    expect(nodes[0].width).toBe(264)
    expect(nodes[0].height).toBe(104)
    // LR：action 排在 objective 右侧，observation 更右
    const pos = Object.fromEntries(nodes.map((n) => [n.id, n.position]))
    expect(pos['n-act-a'].x).toBeGreaterThan(pos['n-obj'].x)
    expect(pos['n-obs'].x).toBeGreaterThan(pos['n-act-a'].x)

    // 推导链上的 generates 是实线；目标 → 动作的 generates 是归属（点线）
    expect(edges.find((e) => e.id.startsWith('n-act-a-n-obs'))?.style?.strokeDasharray).toBeUndefined()
    expect(edges.find((e) => e.id.startsWith('n-obj-n-act-a'))?.style?.strokeDasharray).toBe(EDGE_LOOKS.ownership.dash)
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

// 归属（目标 → 它名下的动作）每个动作都有，大图上是从根目标铺开的扇形：画成细点线，
// 与推导链（动作 → 观测 → 结果）的实线区分主次。
describe('边的画法', () => {
  it('目标指向动作的 generates 是归属（细点线），其余 generates 是推导（实线）', () => {
    const { edges } = buildFlowGraph(GRAPH, 'all', 'all', new Map())
    const ownership = edges.find((e) => e.id.startsWith('n-obj-n-act-a'))!
    const derive = edges.find((e) => e.id.startsWith('n-act-a-n-obs'))!
    expect(ownership.style?.strokeWidth).toBeLessThan(derive.style?.strokeWidth as number)
    expect(ownership.style?.strokeDasharray).toBe(EDGE_LOOKS.ownership.dash)
    expect(derive.style?.strokeDasharray).toBeUndefined()
    // 样本与画法同源：图例画出来的就是画布上的线
    expect(EDGE_LOOK_LABELS).toMatchObject({
      ownership: '归属',
      generates: '推导',
      triggers: '滋生',
      supersedes: '吸收',
      serves: '推进',
      pursues: '跟进',
      attaches: '挂接',
    })
  })

  it('推进、跟进、挂接各按自己的线型画；跟进与挂接同色，靠线型分主次', () => {
    const graph: ExplorationGraph = {
      nodes: [
        ...GRAPH.nodes,
        node({ id: 'n-dir', kind: 'objective', source_type: 'planner', content: { description: '换一个登录入口' } }),
        node({ id: 'n-lead', kind: 'observation', content: { statement: '探通了一条路' } }),
      ],
      edges: [
        { src_id: 'n-act-b', dst_id: 'n-dir', rel: 'serves', created_at: '' },
        { src_id: 'n-act-b', dst_id: 'n-lead', rel: 'pursues', created_at: '' },
        { src_id: 'n-act-a', dst_id: 'n-lead', rel: 'attaches', created_at: '' },
      ],
    }
    const { edges } = buildFlowGraph(graph, 'all', 'all', new Map())
    const byRel = (rel: string) => edges.find((e) => e.id.includes(`-${rel}-`))!
    expect(byRel('serves').style).toMatchObject({ stroke: EDGE_LOOKS.serves.color, strokeDasharray: EDGE_LOOKS.serves.dash })
    expect(byRel('pursues').style).toMatchObject({ stroke: EDGE_LOOKS.pursues.color })
    expect(byRel('pursues').style?.strokeDasharray).toBeUndefined()
    expect(byRel('attaches').style).toMatchObject({ stroke: EDGE_LOOKS.attaches.color, strokeDasharray: EDGE_LOOKS.attaches.dash })
    expect(EDGE_LOOKS.attaches.width).toBeLessThan(EDGE_LOOKS.pursues.width)
    expect(EDGE_LOOKS.attaches.color).not.toBe(EDGE_LOOKS.ownership.color)
  })

  it('只看方向关系：收缩到滋生、吸收、推进三种边及它们两端的节点；跟进与挂接不入内', () => {
    const graph: ExplorationGraph = {
      ...GRAPH,
      nodes: [
        ...GRAPH.nodes,
        node({ id: 'n-dir', kind: 'objective', source_type: 'planner', content: { description: '换一个登录入口' } }),
        node({ id: 'n-lead', kind: 'observation', content: { statement: '探通了一条路' } }),
      ],
      edges: [
        ...GRAPH.edges,
        { src_id: 'n-res', dst_id: 'n-dir', rel: 'triggers', created_at: '' },
        { src_id: 'n-act-b', dst_id: 'n-dir', rel: 'serves', created_at: '' },
        { src_id: 'n-act-b', dst_id: 'n-lead', rel: 'pursues', created_at: '' },
      ],
    }
    const all = buildFlowGraph(graph, 'all', 'all', new Map())
    expect(all.nodes).toHaveLength(7)
    expect(all.stats.directionEdgeCount).toBe(3)

    const { nodes, edges } = buildFlowGraph(graph, 'all', 'all', new Map(), true)
    // 剩下结果、两个目标与为方向出力的那个动作；只连着推导与归属的动作、观测，以及只被跟进的线索不在内
    expect(nodes.map((n) => n.id).sort()).toEqual(['n-act-b', 'n-dir', 'n-obj', 'n-res'])
    expect(edges.map((e) => e.id)).toEqual(['n-res-n-obj-triggers-0', 'n-res-n-dir-triggers-1', 'n-act-b-n-dir-serves-2'])
    // 统计仍是全量：筛选只影响画布可见性
    expect(buildFlowGraph(graph, 'all', 'all', new Map(), true).stats.total).toBe(7)
  })

  it('只看方向关系时照旧吃节点种类与状态筛选', () => {
    const { nodes } = buildFlowGraph(GRAPH, 'result', 'all', new Map(), true)
    expect(nodes.map((n) => n.id)).toEqual(['n-res'])
  })
})
