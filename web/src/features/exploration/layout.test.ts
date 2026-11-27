import { describe, expect, it } from 'vitest'
import type { Edge } from '@xyflow/react'
import { applyFocus, buildFlowGraph, EDGE_LOOKS, EDGE_LOOK_LABELS, EDGE_LOOK_ORDER, owningDirection } from './layout'
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
//   结果 r 催生方向 d1；a3 挂在 d1 下；a4 在办，自己产出了线索 l2
const CHAIN: ExplorationGraph = {
  nodes: [
    node(),
    node({ id: 'a1', kind: 'action', state: 'completed', content: { instruction: '摸入口' } }),
    node({ id: 'l1', kind: 'observation', content: { statement: '探通一条路' }, metadata: { opens_deeper: true } }),
    node({ id: 'a2', kind: 'action', state: 'completed', content: { instruction: '沿这条路深入' } }),
    node({ id: 'o2', kind: 'observation', content: { statement: '拿到了凭据' } }),
    node({ id: 'r', kind: 'result', content: { statement: '弱口令坐实' } }),
    node({ id: 'd1', source_type: 'planner', state: 'open', content: { description: '换一个登录入口' } }),
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
    edge('d1', 'a3', 'generates'),
    edge('n-obj', 'a4', 'generates'),
    edge('a4', 'l2', 'generates'),
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

    // 五条边全部画出（含目标 → 动作的两段）
    expect(stats).toEqual({
      total: 5,
      edgeCount: 5,
      byKind: { objective: 1, action: 2, observation: 1, result: 1 },
      byState: { running: 1, open: 1 },
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
    // 滋生：结果 → 它催生的节点，琥珀短虚线
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
  it('目标 → 动作的 generates 常显：动作挂在哪个目标（方向）下，靠这条线画出来', () => {
    const ownership = byId(buildFlowGraph(GRAPH, 'all', 'all', new Map()).edges, 'n-obj-n-act-b-generates-1')
    expect(ownership.hidden).toBeUndefined()
    expect(ownership).toMatchObject({ source: 'n-obj', target: 'n-act-b', style: { stroke: EDGE_LOOKS.generates.color } })
    expect(Object.keys(EDGE_LOOKS).sort()).toEqual([...EDGE_LOOK_ORDER].sort())
    expect(EDGE_LOOK_LABELS).toEqual({ generates: '推导', triggers: '滋生', pursues: '跟进' })
  })

  it('每种关系一个线型，不靠颜色也分得开', () => {
    const dashes = EDGE_LOOK_ORDER.map((look) => EDGE_LOOKS[look].dash ?? '实线')
    expect(new Set(dashes).size).toBe(dashes.length)
    expect(EDGE_LOOKS.pursues.dash).toBe('10 4 2 4')
  })

  it('跟进由线索指向跟进它的动作，青色点划线', () => {
    const { edges } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    expect(byId(edges, 'a2-l1-pursues-3')).toMatchObject({
      source: 'l1',
      target: 'a2',
      style: { stroke: EDGE_LOOKS.pursues.color, strokeDasharray: '10 4 2 4' },
    })
  })

  it('布局跟着时间走：跟进的动作排在线索右侧，方向下的动作排在方向右侧', () => {
    const { nodes } = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const pos = positions(nodes)
    expect(pos.a2).toBeGreaterThan(pos.l1)
    expect(pos.a3).toBeGreaterThan(pos.d1)
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
      a3: 'exp-dimmed',
      a4: 'exp-dimmed',
      l2: 'exp-dimmed',
    })
    const lit = view.edges.filter((e) => e.style?.strokeWidth === 2.4).map((e) => e.id)
    expect(lit).toEqual(['n-obj-a1-generates-0', 'n-obj-a2-generates-1', 'a1-l1-generates-2', 'a2-l1-pursues-3', 'a2-o2-generates-4'])
    // 其余的线淡出（透明色后缀），没有哪条线被藏起来
    expect(view.edges.every((e) => e.hidden !== true)).toBe(true)
    expect(byId(view.edges, 'r-d1-triggers-5').style?.stroke).toMatch(/22$/)
  })

  it('挂在方向下的动作：来路经过方向，再到催生它的结果', () => {
    const view = applyFocus(buildFlowGraph(CHAIN, 'all', 'all', new Map()), 'a3')
    expect(classes(view.nodes)).toMatchObject({ a3: 'exp-selected', d1: 'exp-path', r: 'exp-path', 'n-obj': 'exp-dimmed', a1: 'exp-dimmed' })
    expect(byId(view.edges, 'd1-a3-generates-6').style?.strokeWidth).toBe(2.4)
    expect(byId(view.edges, 'r-d1-triggers-5').style?.strokeWidth).toBe(2.4)
  })

  it('直接挂在根目标下的动作：来路只有根目标到它这一段', () => {
    const view = applyFocus(buildFlowGraph(CHAIN, 'all', 'all', new Map()), 'a4')
    expect(classes(view.nodes)).toMatchObject({ a4: 'exp-selected', 'n-obj': 'exp-path', l2: 'exp-dimmed', a1: 'exp-dimmed', a3: 'exp-dimmed' })
    // a4 自己产出的东西在它之后，不在来路上
    expect(byId(view.edges, 'a4-l2-generates-8').style?.stroke).toMatch(/22$/)
    expect(byId(view.edges, 'n-obj-a4-generates-7')).toMatchObject({ style: { strokeWidth: 2.4 } })
  })

  it('一直走到头为止：指向根目标的边也算', () => {
    const view = applyFocus(buildFlowGraph(GRAPH, 'all', 'all', new Map()), 'n-act-b')
    expect(classes(view.nodes)).toEqual({
      'n-obj': 'exp-path',
      'n-act-a': 'exp-path',
      'n-act-b': 'exp-selected',
      'n-obs': 'exp-path',
      'n-res': 'exp-path',
    })
    // 来路上的每条线都加粗：目标 → 两个动作、动作 → 观察、观察 → 结果、结果 → 根目标
    expect(view.edges.filter((e) => e.style?.strokeWidth === 2.4)).toHaveLength(5)
  })

  it('取消选中后恢复全图', () => {
    const flow = buildFlowGraph(CHAIN, 'all', 'all', new Map())
    const cleared = applyFocus(flow, '')
    expect(cleared.nodes.every((n) => !n.className)).toBe(true)
    expect(cleared.edges.every((e) => e.style?.strokeWidth !== 2.4)).toBe(true)
    // 取消选中不改动线的可见性：九条线都在
    expect(cleared.edges).toHaveLength(9)
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

// 执行中不是一种关系，是一种活动状态：连到执行中动作的线流动，其余的线照常画着。
describe('执行中', () => {
  it('连到执行中动作的线流动；待执行动作的线不流动，但一样画着', () => {
    const { edges } = buildFlowGraph(GRAPH, 'all', 'all', new Map())
    expect(byId(edges, 'n-obj-n-act-a-generates-0')).toMatchObject({ animated: true })
    expect(byId(edges, 'n-act-a-n-obs-generates-2').animated).toBe(true)
    expect(byId(edges, 'n-obj-n-act-b-generates-1')).toMatchObject({ animated: false })
    expect(edges.every((e) => e.hidden !== true)).toBe(true)
  })

  it('动作结束后线不再流动', () => {
    const done: ExplorationGraph = {
      ...GRAPH,
      nodes: GRAPH.nodes.map((n) => (n.id === 'n-act-a' ? { ...n, state: 'completed' } : n)),
    }
    const { edges, stats } = buildFlowGraph(done, 'all', 'all', new Map())
    expect(edges.some((e) => e.animated)).toBe(false)
    expect(stats.edgeCount).toBe(5)
    expect(stats.byState).toEqual({ open: 1, completed: 1 })
  })
})
