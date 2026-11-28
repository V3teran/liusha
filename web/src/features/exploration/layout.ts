/**
 * 探索图 → React Flow 元素构建 + dagre 自左向右（LR）布局 + 来路高亮。
 *
 * 布局只保留 LR：推导链（objective→action→observation→result）从左到右
 * 符合阅读顺序，深度随任务推进向右增长，同层节点垂直堆叠利用屏幕高度，
 * 宽扁文本卡片在 TB 布局下会水平铺成不可读的横带。
 * 卡片尺寸固定（与 ExplorationNodeCard 严格一致），dagre 估算即精确，
 * 连线不会错位。
 *
 * 横轴是时间：越靠右的节点出现得越晚，一条来路在图上就是一段自左向右的连线。库里的边
 * 不都是这个方向（跟进由动作指向线索），画的时候按 UPSTREAM 调转——箭头一律指向更晚
 * 的节点。
 *
 * 图上有三种关系，画成三种线：
 *  - 推导（generates），中性实线（颜色留给节点，避免满屏彩虹）：目标 → 挂在它名下的动作、
 *    动作 → 观测 → 结果。目标 → 动作那一段是方向的分组在图上的样子，常显
 *  - 滋生：结果 → 方向 / 动作（triggers），琥珀短虚线——坐实的结果催生了新方向或新动作
 *  - 跟进：线索 → 跟进它的动作（pursues），青色点划线——动作为追这条线索而排出
 *
 * 每种关系一个线型，不靠颜色也分得开（实线 / 短虚线 / 点划线）。
 *
 * 执行中不是一个关系，是一种活动状态：连到执行中动作的线流动。
 */

import dagre from '@dagrejs/dagre'
import { MarkerType, type BuiltInEdge, type Edge, type Node } from '@xyflow/react'
import type { ExplorationGraph, GraphEdge, GraphNode, Verification } from '@/api/explorationgraph'

export const NODE_WIDTH = 264
export const NODE_HEIGHT = 104 // h-26

export type ExplorationNodeData = {
  node: GraphNode
  verification?: Verification
  /** 已关闭的线索是否被动作认领过（存在 pursues 边）：灰脚印与无标记的分界 */
  leadFollowed?: boolean
  /** 仍开着的线索是否正被开跑中的动作认领（存在认领动作的 pursues 边）：青脚印与橙脚印的分界 */
  leadActive?: boolean
} & Record<string, unknown>

export type ExplorationFlowNode = Node<ExplorationNodeData, 'exploration'>

export interface GraphStats {
  total: number
  /** 画出来的连线数（= 全部边） */
  edgeCount: number
  byKind: Record<string, number>
  /** 各状态的节点数（全量）：状态筛选只列这张图里实际有的状态 */
  byState: Record<string, number>
}

/** 图上的三种关系（库里的 rel）。 */
export type Relation = 'generates' | 'triggers' | 'pursues'

/**
 * 每种关系里时间上在先的一端（库里的 src / dst）。画出来时它在左、箭头指向另一端，
 * 来路高亮沿它逆行。
 */
const UPSTREAM: Record<Relation, 'src' | 'dst'> = {
  generates: 'src',
  triggers: 'src',
  pursues: 'dst',
}

/** 边的画法，一种关系一种。 */
export type EdgeLook = Relation

export interface EdgeLookStyle {
  color: string
  width: number
  /** 虚线节奏；不写即实线 */
  dash?: string
  /** 箭头大小（与线宽相称） */
  marker: number
}

// 边色是亮暗双主题通用的固定中间调（节点/连线墨水的既定用色口径），
// 取值以「两主题背景上均 ≥3:1 图形对比度」为硬约束（浅色背景 oklch(98.5% 0 0)、深色
// oklch(14% 0.004 285) 上实测）：
//  - 推导用 slate-500：slate-400 在浅色主题仅 2.4:1 会没入背景
//  - 滋生用 amber-600：它是全图里少见的彩色关系边，amber-500 浅色下仅 2:1
//  - 跟进用 teal-600（浅 3.6、深 5.2）：线索与动作之间的关系
export const EDGE_LOOKS: Record<EdgeLook, EdgeLookStyle> = {
  generates: { color: '#64748b', width: 1.6, marker: 14 },
  triggers: { color: '#d97706', width: 1.6, dash: '6 4', marker: 14 },
  pursues: { color: '#0d9488', width: 1.8, dash: '10 4 2 4', marker: 14 },
}

/** 图例的顺序。 */
export const EDGE_LOOK_ORDER: readonly EdgeLook[] = ['generates', 'triggers', 'pursues']

export const EDGE_LOOK_LABELS: Record<EdgeLook, string> = {
  generates: '推导',
  triggers: '滋生',
  pursues: '跟进',
}

/** 一条边该用哪种画法：认识的关系各按自己的线型，不认识的按推导画（照常显示）。 */
export function edgeLook(e: GraphEdge): EdgeLook {
  return e.rel in EDGE_LOOKS ? (e.rel as Relation) : 'generates'
}

/** 这条边连着的动作里有没有正在执行的：连到执行中动作的线流动。 */
function isRunning(e: GraphEdge, stateById: Map<string, string>): boolean {
  return stateById.get(e.src_id) === 'running' || stateById.get(e.dst_id) === 'running'
}

/**
 * 动作所属的方向：挂在派生目标下的动作返回那个目标；直接挂在根目标下的动作、不是动作的
 * 节点，没有。
 */
export function owningDirection(graph: ExplorationGraph, actionId: string): GraphNode | undefined {
  const edge = graph.edges.find((e) => e.rel === 'generates' && e.dst_id === actionId)
  if (!edge) return undefined
  const owner = graph.nodes.find((n) => n.id === edge.src_id)
  return owner?.kind === 'objective' && owner.source_type === 'planner' ? owner : undefined
}

/**
 * 把任务的探索图变成画布上的节点与边。
 *
 * 交给画布的边一律从时间上在先的一端指向在后的（见 UPSTREAM），布局也按这个方向排。
 */
export function buildFlowGraph(
  graph: ExplorationGraph,
  filterKind: string,
  filterState: string,
  verificationsByNode: Map<string, Verification>,
): { nodes: ExplorationFlowNode[]; edges: Edge[]; stats: GraphStats } {
  // 统计始终基于全量数据（筛选只影响画布可见性）
  const stateById = new Map(graph.nodes.map((n) => [n.id, n.state ?? '']))
  const byKind: Record<string, number> = {}
  const byState: Record<string, number> = {}
  for (const n of graph.nodes) {
    byKind[n.kind] = (byKind[n.kind] ?? 0) + 1
    if (n.state) byState[n.state] = (byState[n.state] ?? 0) + 1
  }
  const stats: GraphStats = { total: graph.nodes.length, edgeCount: graph.edges.length, byKind, byState }

  const visible = new Set(
    graph.nodes
      .filter((n) => (filterKind === 'all' || n.kind === filterKind) && (filterState === 'all' || n.state === filterState))
      .map((n) => n.id),
  )

  // pursues 边是认领的记录（认领即授权，动作结束边仍在）：
  //   - 曾被认领（任一 pursues 边）→ 已关闭的线索留灰脚印，因果在图上看得见；
  //   - 正被认领（认领它的动作还在开跑）→ 仍开着的线索换青脚印，青色与跟进连线同色。
  // 没有认领边就关闭的（规划者判不值得 / 被别的动作覆盖的追认）不算走过，不留脚印。
  const followedLeads = new Set<string>()
  const activeLeads = new Set<string>()
  for (const e of graph.edges) {
    if (e.rel !== 'pursues') continue
    followedLeads.add(e.dst_id)
    const st = stateById.get(e.src_id)
    if (st === 'open' || st === 'running') activeLeads.add(e.dst_id)
  }

  const nodes: ExplorationFlowNode[] = graph.nodes
    .filter((n) => visible.has(n.id))
    .map((n) => ({
      id: n.id,
      type: 'exploration' as const,
      position: { x: 0, y: 0 },
      // 显式尺寸是硬要求：MiniMap 只画带声明尺寸的节点（DOM 测量的结果不会
      // 回写到受控 node 对象上），同时让 dagre 与渲染共用同一份尺寸假设
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      data: {
        node: n,
        verification: verificationsByNode.get(n.id),
        leadFollowed: followedLeads.has(n.id),
        leadActive: activeLeads.has(n.id),
      },
    }))

  // BuiltInEdge：smoothstep 的 pathOptions 只在联合类型的对应分支上（基础 Edge 无此字段）
  const edges: BuiltInEdge[] = graph.edges
    .filter((e) => visible.has(e.src_id) && visible.has(e.dst_id))
    .map((e, idx) => {
      const look = EDGE_LOOKS[edgeLook(e)]
      const [from, to] = UPSTREAM[edgeLook(e)] === 'dst' ? [e.dst_id, e.src_id] : [e.src_id, e.dst_id]
      return {
        id: `${e.src_id}-${e.dst_id}-${e.rel}-${idx}`,
        source: from,
        target: to,
        type: 'smoothstep',
        pathOptions: { borderRadius: 14 },
        // 连到执行中动作的线流动：图是运行时探索图，活跃工作可感知
        animated: isRunning(e, stateById),
        style: { stroke: look.color, strokeWidth: look.width, ...(look.dash ? { strokeDasharray: look.dash } : {}) },
        markerEnd: { type: MarkerType.ArrowClosed, width: look.marker, height: look.marker, color: look.color },
      }
    })

  return { nodes: layoutLR(nodes, edges), edges, stats }
}

function layoutLR(nodes: ExplorationFlowNode[], edges: Edge[]): ExplorationFlowNode[] {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 28, ranksep: 96, marginx: 24, marginy: 24 })
  nodes.forEach((n) => g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT }))
  edges.forEach((e) => g.setEdge(e.source, e.target))
  dagre.layout(g)
  return nodes.map((n) => {
    const p = g.node(n.id)
    return { ...n, position: { x: p.x - NODE_WIDTH / 2, y: p.y - NODE_HEIGHT / 2 } }
  })
}

/**
 * 来路高亮：选中节点后，从根目标到它的每一条来路上的节点保持常亮、连线加粗，其余
 * 节点/边淡出——在百节点图上直接回答「这个节点是怎么一步步来的」。
 *
 * 来路沿时间逆行：产出它的、它跟进的线索、催生它的结果或方向，一路走到没有来路为止；
 * 一个节点有几条来路就都点亮。
 */
export function applyFocus(
  flow: { nodes: ExplorationFlowNode[]; edges: Edge[] },
  selectedNodeId: string,
): { nodes: ExplorationFlowNode[]; edges: Edge[] } {
  if (!selectedNodeId) {
    return {
      nodes: flow.nodes.map((n) => (n.className ? { ...n, className: undefined } : n)),
      edges: flow.edges,
    }
  }

  // 来路上的边按「指向谁」归堆，从选中节点一路逆着箭头走回去。
  const incoming = new Map<string, Edge[]>()
  for (const e of flow.edges) {
    const list = incoming.get(e.target)
    if (list) list.push(e)
    else incoming.set(e.target, [e])
  }
  const originNodes = new Set([selectedNodeId])
  const originEdgeIds = new Set<string>()
  const queue = [selectedNodeId]
  for (let i = 0; i < queue.length; i++) {
    for (const e of incoming.get(queue[i]) ?? []) {
      originEdgeIds.add(e.id)
      if (!originNodes.has(e.source)) {
        originNodes.add(e.source)
        queue.push(e.source)
      }
    }
  }

  return {
    nodes: flow.nodes.map((n) => {
      if (n.id === selectedNodeId) return { ...n, className: 'exp-selected' }
      return originNodes.has(n.id) ? { ...n, className: 'exp-path' } : { ...n, className: 'exp-dimmed' }
    }),
    edges: flow.edges.map((e) => {
      const base = e.style ?? {}
      if (originEdgeIds.has(e.id)) return { ...e, style: { ...base, strokeWidth: 2.4 } }
      // 线与箭头同淡（opacity 不作用于 marker，改用透明色）
      const dim = (c: string) => c + '22'
      return {
        ...e,
        style: { ...base, stroke: dim(typeof base.stroke === 'string' ? base.stroke : EDGE_LOOKS.generates.color) },
        markerEnd:
          e.markerEnd && typeof e.markerEnd === 'object'
            ? { ...e.markerEnd, color: dim(typeof e.markerEnd.color === 'string' ? e.markerEnd.color : EDGE_LOOKS.generates.color) }
            : e.markerEnd,
      }
    }),
  }
}
