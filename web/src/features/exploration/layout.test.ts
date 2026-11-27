import { describe, expect, it } from 'vitest'
import type { Edge } from '@xyflow/react'
import {
  applyFocus,
  buildFlowGraph,
  EDGE_LOOKS,
  EDGE_LOOK_LABELS,
  EDGE_LOOK_ORDER,
  hasRunningEdges,
  owningDirection,
  shownRelations,
} from './layout'
import type { ExplorationGraph, GraphEdge, GraphNode } from '@/api/explorationgraph'

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

function edge(src_id: string, dst_id: string, rel: string): GraphEdge {
  return { src_id, dst_id, rel, created_at: '2026-06-10T00:00:00Z' }
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
    edge('n-obj', 'n-act-a', 'generates'),
    edge('n-obj', 'n-act-b', 'generates'),
    edge('n-act-a', 'n-obs', 'generates'),
    edge('n-obs', 'n-res', 'generates'),
    edge('n-res', 'n-obj', 'triggers'),
  ],
}

// 一条深挖链加一个方向：
//   根目标 → a1 → 线索 l1 → a2（跟进 l1）→ o2
//   结果 r 催生方向 d1；d2 接管 d1；a3 挂在 d1 下
//   a4 在办，自己产出了线索 l2 并覆盖它（只记账）
const CHAIN: ExplorationGraph = {
  nodes: [
    node(),
    node({ id: 'a1', kind: 'action', state: 'completed', content: { instruction: '摸入口' } }),
    node({ id: 'l1', kind: 'observation', content: { statement: '探通一条路' }, metadata: { opens_deeper: true } }),
    node({ id: 'a2', kind: 'action', state: 'completed', content: { instruction: '沿这条路深入' } }),
    node({ id: 'o2', kind: 'observation', content: { statement: '拿到了凭据' } }),
    node({ id: 'r', kind: 'result', content: { statement: '弱口令坐实' } }),
    node({ id: 'd1', source_type: 'planner', state: 'open', content: { description: '换一个登录入口' } }),
    node({ id: 'd2', source_type: 'planner', state: 'open', content: { description: '枚举全部入口' } }),
    node({ id: 'a3', kind: 'action', state: 'open', content: { instruction: '试另一个入口' } }),
    node({ id: 'a4', kind: 'action', state: 'running', content: { instruction: '翻上传页' } }),
    node({ id: 'l2', kind: 'observation', content: { statement: '又探通一条' }, metadata: { opens_deeper: true } }),
  ],
  edges: [
    edge('n-obj', 'a1', 'generates'),
    edge('n-obj', 'a2', 'generates'),
    edge('a1', 'l1', 'generates'),
    edge('a2', 'l1', 'pursues'),
    edge('a2', 'o2', 'generates'),
    edge('r', 'd1', 'triggers'),
    edge('d2', 'd1', 'supersedes'),
    edge('d1', 'a3', 'generates'),
    edge('n-obj', 'a4', 'generates'),
    edge('a4', 'l2', 'generates'),
    edge('a4', 'l2', 'covers'),
  ],
}

const positions = (nodes: { id: string; position: { x: number } }[]) =>
  Object.fromEntries(nodes.map((n) => [n.id, n.position.x]))
const byId = (edges: Edge[], id: string) => edges.find((e) => e.id === id)!
const classes = (nodes: { id: string; className?: string }[]) =>
  Object.fromEntries(nodes.map((n) => [n.id, n.className]))

describe('layout（LR 布局 + 边关系 + 来路高亮）', () => {
  it('buildFlowGraph：LR 方向、执行中线流动、统计全量', () => {
    const { nodes, edges, stats } = buildFlowGraph(GRAPH, 'all', 'all', new Map())

    expect(stats).toEqual({
      total: 5,
      edgeCount: 3,
      byKind: { objective: 1, action: 2, observation: 1, result: 1 },
      directionEdgeCount: 1,
    })
    expect(nodes).toHaveLength(5)
    // 节点声明显式尺寸：MiniMap 渲染的前提，也是 dagre 布局的尺寸假设
    expect(nodes[0].width).toBe(264)
    expect(nodes[0].height).toBe(104)
    // LR：action 排在 objective 右侧，observation 更右
    const pos = positions(nodes)
    expect(pos['n-act-a']).toBeGreaterThan(pos['n-obj'])
    expect(pos['n-obs']).toBeGreaterThan(pos['n-act-a'])

    expect(edges).toHaveLength(5)
    // 推导链上的 generates 是实线，箭头指向更晚的节点
    const chain = byId(edges, 'n-act-a-n-obs-generates-2')
    expect(chain).toMatchObject({ source: 'n-act-a', target: 'n-obs', animated: true })
    expect(chain.style?.strokeDasharray).toBeUndefined()
    // 滋生：结果 → 它催生的节点，琥珀虚线
    expect(byId(edges, 'n-res-n-obj-triggers-4')).toMatchObject({ source: 'n-res', target: 'n-obj' })
    expect(byId(edges, 'n-res-n-obj-triggers-4').style?.strokeDasharray).toBe('6 4')
  })

  it('buildFlowGraph：kind 筛选后剔除悬空边', () => {
    const { nodes, edges, stats } = buildFlowGraph(GRAPH, 'action', 'all', new Map())
    expect(nodes.map((n) => n.id).sort()).toEqual(['n-act-a', 'n-act-b'])
    // generates 边的另一端（objective）被筛掉 → 剔除
    expect(edges).toEqual([])
    expect(stats.byKind.action).toBe(2) // 统计保持全量
  })
})

// 图例与线型同源：一种关系一种线，图例里列出的每一种都画得出来。
describe('边的画法', () => {
  it('目标指向动作的 generates 平时不画：每个动作都挂在某个目标下，这条线不带信息', () => {
    const { edges } = buildFlowGraph(GRAPH, 'all', 'all', new Map())
    // 仍在列表里（布局与来路高亮靠它），只是不画
    const ownership = edges.filter((e) => e.source === 'n-obj' && e.id.includes('-generates-'))
    expect(ownership).toHaveLength(2)
    expect(ownership.every((e) => e.hidden)).toBe(true)
    expect(Object.keys(EDGE_LOOKS).sort()).toEqual([...EDGE_LOOK_ORDER].sort())
    expect(EDGE_LOOK_LABELS).toEqual({
      generates: '推导',
      triggers: '滋生',
      supersedes: '吸收',
      pursues: '跟进',
      covers: '覆盖',
    })
  })

  it('跟进、覆盖各按自己的线型画；两者同色，靠线型分主次', () => {
    const { edges } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    expect(byId(edges, 'a2-l1-pursues-3').style).toMatchObject({ stroke: EDGE_LOOKS.pursues.color })
    expect(byId(edges, 'a2-l1-pursues-3').style?.strokeDasharray).toBeUndefined()
    expect(byId(edges, 'a4-l2-covers-10').style).toMatchObject({
      stroke: EDGE_LOOKS.covers.color,
      strokeDasharray: EDGE_LOOKS.covers.dash,
    })
    expect(EDGE_LOOKS.covers.width).toBeLessThan(EDGE_LOOKS.pursues.width)
    expect(EDGE_LOOKS.covers.color).not.toBe(EDGE_LOOKS.generates.color)
  })

  it('只看方向关系：收缩到滋生与吸收两种边及它们两端的节点；跟进不入内', () => {
    const graph: ExplorationGraph = {
      ...GRAPH,
      nodes: [
        ...GRAPH.nodes,
        node({ id: 'n-dir', kind: 'objective', source_type: 'planner', content: { description: '换一个登录入口' } }),
        node({ id: 'n-lead', kind: 'observation', content: { statement: '探通了一条路' } }),
      ],
      edges: [
        ...GRAPH.edges,
        edge('n-res', 'n-dir', 'triggers'),
        edge('n-act-b', 'n-lead', 'pursues'),
      ],
    }
    const all = buildFlowGraph(graph, 'all', 'all', new Map())
    expect(all.nodes).toHaveLength(7)
    expect(all.stats.directionEdgeCount).toBe(2)

    const { nodes, edges } = buildFlowGraph(graph, 'all', 'all', new Map(), true)
    // 只剩结果与两个目标；动作、观测（只连着推导、跟进与不画的归属边）不在内
    expect(nodes.map((n) => n.id).sort()).toEqual(['n-dir', 'n-obj', 'n-res'])
    expect(edges.map((e) => e.id)).toEqual(['n-res-n-obj-triggers-0', 'n-res-n-dir-triggers-1'])
    // 统计仍是全量：筛选只影响画布可见性
    expect(buildFlowGraph(graph, 'all', 'all', new Map(), true).stats.total).toBe(7)
  })

  it('只看方向关系时照旧吃节点种类与状态筛选', () => {
    const { nodes } = buildFlowGraph(GRAPH, 'result', 'all', new Map(), true)
    expect(nodes.map((n) => n.id)).toEqual(['n-res'])
  })
})

// 横轴是时间：箭头一律指向更晚的节点，越靠右的节点出现得越晚。
describe('时间先后', () => {
  it('跟进由线索指向跟进它的动作，吸收由旧方向指向接管它的新方向', () => {
    const { edges } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    expect(byId(edges, 'a2-l1-pursues-3')).toMatchObject({ source: 'l1', target: 'a2' })
    expect(byId(edges, 'd2-d1-supersedes-6')).toMatchObject({ source: 'd1', target: 'd2' })
    expect(byId(edges, 'a4-l2-covers-10')).toMatchObject({ source: 'l2', target: 'a4' })
    // 库里的两端原样留在 id 与关系上
    expect(byId(edges, 'a2-l1-pursues-3').data).toEqual({ rel: 'pursues' })
  })

  it('布局跟着时间走：跟进的动作排在线索右侧，方向下的动作排在方向右侧，新方向排在旧方向右侧', () => {
    const { nodes } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const pos = positions(nodes)
    expect(pos.a2).toBeGreaterThan(pos.l1)
    expect(pos.a3).toBeGreaterThan(pos.d1)
    expect(pos.d2).toBeGreaterThan(pos.d1)
    expect(pos.l1).toBeGreaterThan(pos.a1)
  })

  it('覆盖只记账，不进布局：在办动作自己产出的线索不会和它成环', () => {
    const { nodes } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const pos = positions(nodes)
    // 只按产出边排：线索在产出它的动作右侧
    expect(pos.l2).toBeGreaterThan(pos.a4)
  })
})

// 选中一个节点，点亮从根目标到它的每一条来路。
describe('来路高亮', () => {
  it('多跳来路整条点亮：产出链、跟进的线索、挂在目标下的那一段', () => {
    const flow = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const view = applyFocus(flow, 'o2')

    // 来路上的节点带 exp-path、选中的带 exp-selected，其余 exp-dimmed：三个层次各有着落
    expect(classes(view.nodes)).toEqual({
      'n-obj': 'exp-path',
      a1: 'exp-path',
      l1: 'exp-path',
      a2: 'exp-path',
      o2: 'exp-selected',
      r: 'exp-dimmed',
      d1: 'exp-dimmed',
      d2: 'exp-dimmed',
      a3: 'exp-dimmed',
      a4: 'exp-dimmed',
      l2: 'exp-dimmed',
    })
    const lit = view.edges.filter((e) => e.style?.strokeWidth === 2.4).map((e) => e.id)
    expect(lit).toEqual([
      'n-obj-a1-generates-0',
      'n-obj-a2-generates-1',
      'a1-l1-generates-2',
      'a2-l1-pursues-3',
      'a2-o2-generates-4',
    ])
    // 平时不画的两段归属边在来路上显形
    expect(byId(view.edges, 'n-obj-a1-generates-0').hidden).toBe(false)
    expect(byId(view.edges, 'n-obj-a2-generates-1').hidden).toBe(false)
    // 不在来路上的归属边照旧不画
    expect(byId(view.edges, 'n-obj-a4-generates-8').hidden).toBe(true)
    // 其余的线淡出（透明色后缀）
    expect(byId(view.edges, 'r-d1-triggers-5').style?.stroke).toMatch(/22$/)
  })

  it('挂在方向下的动作：来路经过方向，再到催生它的结果', () => {
    const view = applyFocus(buildFlowGraph(CHAIN, 'all', 'all', new Map()), 'a3')
    expect(classes(view.nodes)).toMatchObject({ a3: 'exp-selected', d1: 'exp-path', r: 'exp-path', 'n-obj': 'exp-dimmed', d2: 'exp-dimmed' })
    expect(byId(view.edges, 'd1-a3-generates-7').hidden).toBe(false)
    expect(byId(view.edges, 'r-d1-triggers-5').style?.strokeWidth).toBe(2.4)
  })

  it('接管了旧方向的新方向：旧方向在它的来路上', () => {
    const view = applyFocus(buildFlowGraph(CHAIN, 'all', 'all', new Map()), 'd2')
    expect(classes(view.nodes)).toMatchObject({ d2: 'exp-selected', d1: 'exp-path', r: 'exp-path', a3: 'exp-dimmed' })
    expect(byId(view.edges, 'd2-d1-supersedes-6').style?.strokeWidth).toBe(2.4)
  })

  it('直接挂在根目标下的动作：来路只有根目标到它这一段；覆盖只记账，不算来路', () => {
    const view = applyFocus(buildFlowGraph(CHAIN, 'all', 'all', new Map()), 'a4')
    expect(classes(view.nodes)).toMatchObject({
      a4: 'exp-selected',
      'n-obj': 'exp-path',
      l2: 'exp-dimmed',
      a1: 'exp-dimmed',
      a3: 'exp-dimmed',
    })
    expect(byId(view.edges, 'a4-l2-covers-10').style?.stroke).toMatch(/22$/)
    expect(byId(view.edges, 'a4-l2-generates-9').style?.stroke).toMatch(/22$/)
    expect(byId(view.edges, 'n-obj-a4-generates-8')).toMatchObject({ hidden: false, style: { strokeWidth: 2.4 } })
  })

  // 根目标自己也有来路时（图上有边指向它）一并点亮：逆着箭头走到没有来路为止。
  it('一直走到没有来路为止：指向根目标的边也算', () => {
    const view = applyFocus(buildFlowGraph(GRAPH, 'all', 'all', new Map()), 'n-act-b')
    expect(classes(view.nodes)).toEqual({
      'n-obj': 'exp-path',
      'n-act-a': 'exp-path',
      'n-act-b': 'exp-selected',
      'n-obs': 'exp-path',
      'n-res': 'exp-path',
    })
    expect(byId(view.edges, 'n-obj-n-act-a-generates-0').hidden).toBe(false)
  })

  it('取消选中后恢复全图', () => {
    const flow = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const cleared = applyFocus(flow, '')
    expect(cleared.nodes.every((n) => !n.className)).toBe(true)
    // 四段归属边（根目标下的三个动作、方向下的一个）照旧不画
    expect(cleared.edges.filter((e) => e.hidden)).toHaveLength(4)
    expect(cleared.edges.every((e) => e.style?.strokeWidth !== 2.4)).toBe(true)
  })
})

describe('owningDirection', () => {
  it('挂在派生目标下的动作返回那个方向；挂在根目标下的动作与非动作没有', () => {
    expect(owningDirection(CHAIN, 'a3')?.id).toBe('d1')
    expect(owningDirection(CHAIN, 'a1')).toBeUndefined()
    expect(owningDirection(CHAIN, 'l1')).toBeUndefined()
    expect(owningDirection(CHAIN, 'nope')).toBeUndefined()
  })
})

// 图例只列这张图里有的：一种关系在图上一条边都没有，就不该在图例里占一项。
describe('图例的内容', () => {
  it('shownRelations：只返回画出来的关系，按图例顺序；平时不画的归属边不算', () => {
    expect(shownRelations(buildFlowGraph(GRAPH, 'all', 'all', new Map()).edges)).toEqual(['generates', 'triggers'])
    expect(shownRelations(buildFlowGraph(CHAIN, 'all', 'all', new Map()).edges)).toEqual([
      'generates',
      'triggers',
      'supersedes',
      'pursues',
      'covers',
    ])
    expect(shownRelations([])).toEqual([])
  })

  it('hasRunningEdges：有执行中的动作带出来的线才成立', () => {
    expect(hasRunningEdges(buildFlowGraph(GRAPH, 'all', 'all', new Map()).edges)).toBe(true)
    const still: ExplorationGraph = {
      ...GRAPH,
      nodes: GRAPH.nodes.map((n) => (n.id === 'n-act-a' ? { ...n, state: 'completed' } : n)),
    }
    expect(hasRunningEdges(buildFlowGraph(still, 'all', 'all', new Map()).edges)).toBe(false)
  })
})
