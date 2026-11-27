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
 * 不都是这个方向（跟进、覆盖由动作指向线索，吸收由新方向指向旧方向），画的时候按
 * UPSTREAM 调转——箭头一律指向更晚的节点。
 *
 * 图上有五种关系，画成五种线：
 *  - 推导（generates），中性实线（颜色留给节点，避免满屏彩虹）：动作 → 观测 → 结果，
 *    以及目标 → 挂在它名下的动作。后者平时不画（hidden）：每个动作都有这条边，大图上
 *    只是从根目标铺开的一片扇形；它留在边的列表里——布局靠它把目标排在动作左侧，来路
 *    高亮靠它把动作接回目标，并且只在来路上才画出来
 *  - 滋生：结果 → 目标 / 动作（triggers），琥珀虚线——坐实的结果催生了新方向或新动作
 *  - 吸收：旧派生目标 → 接管它的新派生目标（supersedes），紫色长虚线
 *  - 跟进：线索 → 跟进它的动作（pursues），青色实线——动作为追这条线索而排出
 *  - 覆盖：线索 → 覆盖它的在办动作（covers），青色细点线——线索晚出现，在办动作本来
 *    就在它上，只记账
 */

import dagre from '@dagrejs/dagre'
import { MarkerType, type BuiltInEdge, type Edge, type Node } from '@xyflow/react'
import type { ExplorationGraph, GraphEdge, GraphNode, Verification } from '@/api/explorationgraph'

export const NODE_WIDTH = 264
export const NODE_HEIGHT = 104 // h-26

export type ExplorationNodeData = {
  node: GraphNode
  verification?: Verification
} & Record<string, unknown>

export type ExplorationFlowNode = Node<ExplorationNodeData, 'exploration'>

/** 边上记的关系（库里那条边的 rel）；没有的按空串。 */
export function edgeRel(e: Edge): string {
  const rel = (e.data as { rel?: unknown } | undefined)?.rel
  return typeof rel === 'string' ? rel : ''
}

export interface GraphStats {
  total: number
  /** 画出来的连线数：目标 → 动作的归属边平时不画，不计 */
  edgeCount: number
  byKind: Record<string, number>
  /** 滋生与吸收两种边的条数（全量）：没有就不给「只看方向关系」的入口 */
  directionEdgeCount: number
}

/** 图上的五种关系（库里的 rel）。 */
export type Relation = 'generates' | 'triggers' | 'supersedes' | 'pursues' | 'covers'

/**
 * 每种关系里时间上在先的一端（库里的 src / dst）。画出来时它在左、箭头指向另一端，
 * 来路高亮沿它逆行。
 */
const UPSTREAM: Record<Relation, 'src' | 'dst'> = {
  generates: 'src',
  triggers: 'src',
  supersedes: 'dst',
  pursues: 'dst',
  covers: 'dst',
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
//  - 吸收回目标家族色（violet-500，与目标节点的 accent 同谱）：只出现在
//    目标 ↔ 目标之间，长虚线与滋生的短虚线区分
//  - 跟进与覆盖共用 teal-600（浅 3.6、深 5.2）：两者都是线索与动作之间的关系，实线与
//    细点线分主次
export const EDGE_LOOKS: Record<EdgeLook, EdgeLookStyle> = {
  generates: { color: '#64748b', width: 1.6, marker: 14 },
  triggers: { color: '#d97706', width: 1.6, dash: '6 4', marker: 14 },
  supersedes: { color: '#8b5cf6', width: 1.4, dash: '10 6', marker: 14 },
  pursues: { color: '#0d9488', width: 1.8, marker: 14 },
  covers: { color: '#0d9488', width: 1.2, dash: '2 4', marker: 11 },
}

/** 图例的顺序。 */
export const EDGE_LOOK_ORDER: readonly EdgeLook[] = ['generates', 'triggers', 'supersedes', 'pursues', 'covers']

export const EDGE_LOOK_LABELS: Record<EdgeLook, string> = {
  generates: '推导',
  triggers: '滋生',
  supersedes: '吸收',
  pursues: '跟进',
  covers: '覆盖',
}

/**
 * 画布上实际画着的关系，按图例顺序。图例只列这张图里有的：一种关系在图上一条边都没有，
 * 它的线型样例就无从对照，列出来只会让人去找一条不存在的线。
 */
export function shownRelations(edges: Edge[]): EdgeLook[] {
  return EDGE_LOOK_ORDER.filter((look) => edges.some((e) => !e.hidden && edgeRel(e) === look))
}

/** 画布上有没有流动着的线（执行中的动作带出来的）。 */
export function hasRunningEdges(edges: Edge[]): boolean {
  return edges.some((e) => !e.hidden && e.animated === true)
}

/** 滋生与吸收：结果催生方向、方向之间吸收——同是结果分析在图上留下的关系。 */
function isDirectionEdge(e: GraphEdge): boolean {
  return e.rel === 'triggers' || e.rel === 'supersedes'
}

/** 一条边该用哪种画法：认识的关系各按自己的线型，不认识的按推导画（照常显示）。 */
export function edgeLook(e: GraphEdge): EdgeLook {
  return e.rel in EDGE_LOOKS ? (e.rel as Relation) : 'generates'
}

/** 归属边：目标 → 挂在它名下的动作。平时不画；布局与来路高亮认它。 */
function isOwnership(e: GraphEdge, kindById: Map<string, string>): boolean {
  return e.rel === 'generates' && kindById.get(e.src_id) === 'objective'
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
 * directionOnly（只看方向关系）把图收缩到滋生与吸收两种边及它们两端的节点：这两种边一次
 * 结果分析才添几条，在整图里淹没在推导链里；收缩后重排布局，一张图看得全。
 *
 * 交给画布的边一律从时间上在先的一端指向在后的（见 UPSTREAM），布局也按这个方向排。覆盖
 * 只记账，不进布局：在办的动作可能自己就产出那条线索，按时间调转后与它的推导边成环。
 */
export function buildFlowGraph(
  graph: ExplorationGraph,
  filterKind: string,
  filterState: string,
  verificationsByNode: Map<string, Verification>,
  directionOnly = false,
): { nodes: ExplorationFlowNode[]; edges: Edge[]; stats: GraphStats } {
  // 统计始终基于全量数据（筛选只影响画布可见性）
  const kindById = new Map(graph.nodes.map((n) => [n.id, n.kind]))
  const byKind: Record<string, number> = {}
  for (const n of graph.nodes) byKind[n.kind] = (byKind[n.kind] ?? 0) + 1
  const stats: GraphStats = {
    total: graph.nodes.length,
    edgeCount: graph.edges.filter((e) => !isOwnership(e, kindById)).length,
    byKind,
    directionEdgeCount: graph.edges.filter(isDirectionEdge).length,
  }

  const shownEdges = directionOnly ? graph.edges.filter(isDirectionEdge) : graph.edges
  const onShownEdge = new Set(shownEdges.flatMap((e) => [e.src_id, e.dst_id]))
  const visible = new Set(
    graph.nodes
      .filter(
        (n) =>
          (filterKind === 'all' || n.kind === filterKind) &&
          (filterState === 'all' || n.state === filterState) &&
          (!directionOnly || onShownEdge.has(n.id)),
      )
      .map((n) => n.id),
  )

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
      data: { node: n, verification: verificationsByNode.get(n.id) },
    }))

  const stateById = new Map(graph.nodes.map((n) => [n.id, n.state ?? '']))

  // BuiltInEdge：smoothstep 的 pathOptions 只在联合类型的对应分支上（基础 Edge 无此字段）
  const edges: BuiltInEdge[] = shownEdges
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
        // 执行中的动作带流动：图是运行时探索图，活跃工作可感知
        animated: stateById.get(e.src_id) === 'running',
        style: { stroke: look.color, strokeWidth: look.width, ...(look.dash ? { strokeDasharray: look.dash } : {}) },
        markerEnd: { type: MarkerType.ArrowClosed, width: look.marker, height: look.marker, color: look.color },
        data: { rel: e.rel },
        hidden: isOwnership(e, kindById),
      }
    })

  const laidOut = edges.filter((e) => edgeRel(e) !== 'covers')
  return { nodes: layoutLR(nodes, laidOut), edges, stats }
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
 * 来路沿时间逆行：产出它的、它跟进的线索、催生它的结果或方向、它接管的方向，一路走到
 * 没有来路为止；一个节点有几条来路就都点亮。覆盖只记账，不算来路。平时不画的目标 →
 * 动作那一段，在来路上时画出来，来路因此从根目标起是连着的。
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
    if (edgeRel(e) === 'covers') continue
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
      if (originEdgeIds.has(e.id)) {
        // 平时不画的归属边：在来路上才显形
        return { ...e, hidden: false, style: { ...base, strokeWidth: 2.4 } }
      }
      if (e.hidden) return e
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
