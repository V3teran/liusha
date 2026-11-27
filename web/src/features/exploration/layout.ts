/**
 * 探索图 → React Flow 元素构建 + dagre 自左向右（LR）布局 + 聚焦高亮。
 *
 * 布局只保留 LR：推导链（objective→action→observation→result）从左到右
 * 符合阅读顺序，深度随任务推进向右增长，同层节点垂直堆叠利用屏幕高度，
 * 宽扁文本卡片在 TB 布局下会水平铺成不可读的横带。
 * 卡片尺寸固定（与 ExplorationNodeCard 严格一致），dagre 估算即精确，
 * 连线不会错位。
 *
 * 图上有五种关系，画成六种线：
 *  - 归属：目标 → 它名下的动作（generates）。动作都挂在根目标下，这条边每个动作都有，
 *    大图上是从根目标铺开的一片扇形——画成细点线，不压过携带信息的边
 *  - 推导：动作 → 观测 → 结果（generates），中性实线（颜色留给节点，避免满屏彩虹）
 *  - 滋生：结果 → 目标 / 动作（triggers），琥珀虚线——坐实的结果催生了新方向或新动作
 *  - 吸收：新派生目标 → 被它接管的旧目标（supersedes），紫色长虚线
 *  - 跟进：动作 → 线索（pursues），青色实线——动作为追这条线索而排出
 *  - 覆盖：在办动作 → 线索（covers），青色细点线——线索晚出现，在办动作本来就在它上，只记账
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

export interface GraphStats {
  total: number
  edgeCount: number
  byKind: Record<string, number>
  /** 滋生与吸收两种边的条数（全量）：没有就不给「只看方向关系」的入口 */
  directionEdgeCount: number
}

/** 图上的五种关系（库里的 rel）。 */
export type Relation = 'generates' | 'triggers' | 'supersedes' | 'pursues' | 'covers'

/** 边的画法：归属是 generates 里目标指向动作的那一种。 */
export type EdgeLook = Relation | 'ownership'

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
//  - 归属与推导共用 slate-500：slate-400 在浅色主题仅 2.4:1 会没入背景；两者靠线型
//    （细点线 / 实线）分主次，色相保持克制
//  - 滋生用 amber-600：它是全图里少见的彩色关系边，amber-500 浅色下仅 2:1
//  - 吸收回目标家族色（violet-500，与目标节点的 accent 同谱）：只出现在
//    目标 ↔ 目标之间，长虚线与滋生的短虚线区分
//  - 跟进与覆盖共用 teal-600（浅 3.6、深 5.2）：两者都是动作 → 线索，实线与细点线分主次
export const EDGE_LOOKS: Record<EdgeLook, EdgeLookStyle> = {
  generates: { color: '#64748b', width: 1.6, marker: 14 },
  triggers: { color: '#d97706', width: 1.6, dash: '6 4', marker: 14 },
  supersedes: { color: '#8b5cf6', width: 1.4, dash: '10 6', marker: 14 },
  pursues: { color: '#0d9488', width: 1.8, marker: 14 },
  covers: { color: '#0d9488', width: 1.2, dash: '2 4', marker: 11 },
  ownership: { color: '#64748b', width: 1.1, dash: '2 5', marker: 11 },
}

/** 图例的顺序：先携带信息的关系，归属（每个动作都有）在最后。 */
export const EDGE_LOOK_ORDER: readonly EdgeLook[] = ['generates', 'triggers', 'supersedes', 'pursues', 'covers', 'ownership']

export const EDGE_LOOK_LABELS: Record<EdgeLook, string> = {
  generates: '推导',
  triggers: '滋生',
  supersedes: '吸收',
  pursues: '跟进',
  covers: '覆盖',
  ownership: '归属',
}

/** 滋生与吸收：结果催生方向、方向之间吸收——同是结果分析在图上留下的关系。 */
function isDirectionEdge(e: GraphEdge): boolean {
  return e.rel === 'triggers' || e.rel === 'supersedes'
}

/**
 * 一条边该用哪种画法：目标指向动作的 generates 是归属；其余认识的关系各按自己的线型，
 * 不认识的按推导画（照常显示）。
 */
export function edgeLook(e: GraphEdge, kindById: Map<string, string>): EdgeLook {
  if (e.rel === 'generates' && kindById.get(e.src_id) === 'objective') return 'ownership'
  return e.rel in EDGE_LOOKS && e.rel !== 'generates' && e.rel !== 'ownership' ? (e.rel as Relation) : 'generates'
}

/**
 * 把任务的探索图变成画布上的节点与边。
 *
 * directionOnly（只看方向关系）把图收缩到滋生与吸收两种边及它们两端的节点：这两种边一次
 * 结果分析才添几条，在整图里淹没在推导链与归属的扇形里；收缩后重排布局，一张图看得全。
 */
export function buildFlowGraph(
  graph: ExplorationGraph,
  filterKind: string,
  filterState: string,
  verificationsByNode: Map<string, Verification>,
  directionOnly = false,
): { nodes: ExplorationFlowNode[]; edges: Edge[]; stats: GraphStats } {
  // 统计始终基于全量数据（筛选只影响画布可见性）
  const byKind: Record<string, number> = {}
  for (const n of graph.nodes) byKind[n.kind] = (byKind[n.kind] ?? 0) + 1
  const stats: GraphStats = {
    total: graph.nodes.length,
    edgeCount: graph.edges.length,
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
  const kindById = new Map(graph.nodes.map((n) => [n.id, n.kind]))

  // BuiltInEdge：smoothstep 的 pathOptions 只在联合类型的对应分支上（基础 Edge 无此字段）
  const edges: BuiltInEdge[] = shownEdges
    .filter((e) => visible.has(e.src_id) && visible.has(e.dst_id))
    .map((e, idx) => {
      const look = EDGE_LOOKS[edgeLook(e, kindById)]
      return {
        id: `${e.src_id}-${e.dst_id}-${e.rel}-${idx}`,
        source: e.src_id,
        target: e.dst_id,
        type: 'smoothstep',
        pathOptions: { borderRadius: 14 },
        // 执行中动作的出边流动：图是运行时探索图，活跃工作可感知
        animated: stateById.get(e.src_id) === 'running',
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
 * 聚焦高亮：选中节点后，与其相连的边加粗、一二级邻居保持常亮，
 * 其余节点/边淡出——在百节点图上直接回答「这个节点和谁有关系」。
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

  const connected = flow.edges.filter((e) => e.source === selectedNodeId || e.target === selectedNodeId)
  const focusNodeIds = new Set([selectedNodeId, ...connected.flatMap((e) => [e.source, e.target])])
  const focusEdgeIds = new Set(connected.map((e) => e.id))

  return {
    nodes: flow.nodes.map((n) => {
      if (n.id === selectedNodeId) return { ...n, className: 'exp-selected' }
      return focusNodeIds.has(n.id) ? { ...n, className: undefined } : { ...n, className: 'exp-dimmed' }
    }),
    edges: flow.edges.map((e) => {
      const base = e.style ?? {}
      if (focusEdgeIds.has(e.id)) {
        return { ...e, style: { ...base, strokeWidth: 2.4 } }
      }
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
