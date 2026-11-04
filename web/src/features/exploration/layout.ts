/**
 * 探索图 → React Flow 元素构建 + dagre 自左向右（LR）布局 + 聚焦高亮。
 *
 * 布局只保留 LR：推导链（objective→action→observation→result）从左到右
 * 符合阅读顺序，深度随任务推进向右增长，同层节点垂直堆叠利用屏幕高度，
 * 宽扁文本卡片在 TB 布局下会水平铺成不可读的横带。
 * 卡片尺寸固定（与 ExplorationNodeCard 严格一致），dagre 估算即精确，
 * 连线不会错位。
 *
 * 边的三种关系：
 *  - generates 推导：中性实线（颜色留给节点，避免满屏彩虹）
 *  - triggers  滋生：琥珀虚线，发现坐实后派生新目标/动作的回边一眼可辨
 *  - depends_on 依赖：点线，从 action 节点的 metadata.depends_on 派生
 *    （后端 0184 后调度依赖不走边表，本模块在展示层还原为边）
 */

import dagre from '@dagrejs/dagre'
import { MarkerType, type BuiltInEdge, type Edge, type Node } from '@xyflow/react'
import type { ConversationGraph, GraphNode, Verification } from '@/api/explorationgraph'

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
}

// 边色是亮暗双主题通用的固定中间调（节点/连线墨水的既定用色口径），
// 取值以「两主题背景上均 ≥3:1 图形对比度」为硬约束：
//  - 推导/依赖共用 slate-500：slate-400 在浅色主题仅 2.4:1 会没入背景，
//    且让次要的依赖边反而比主干推导边更显眼（层级倒置）；
//    两者靠线型（实线/点线）区分，色相保持克制
//  - 滋生用 amber-600：它是全图唯一的彩色关系边，amber-500 浅色下仅 2:1
export const EDGE_COLORS = {
  generates: '#64748b',
  depends: '#64748b',
  triggers: '#d97706',
} as const

export function buildFlowGraph(
  graph: ConversationGraph,
  filterKind: string,
  filterState: string,
  verificationsByNode: Map<string, Verification>,
): { nodes: ExplorationFlowNode[]; edges: Edge[]; stats: GraphStats } {
  // 统计始终基于全量数据（筛选只影响画布可见性）
  const byKind: Record<string, number> = {}
  for (const n of graph.nodes) byKind[n.kind] = (byKind[n.kind] ?? 0) + 1
  const stats: GraphStats = { total: graph.nodes.length, edgeCount: graph.edges.length, byKind }

  const visible = new Set(
    graph.nodes
      .filter(
        (n) =>
          (filterKind === 'all' || n.kind === filterKind) &&
          (filterState === 'all' || n.state === filterState),
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

  const styled = (color: string, strokeWidth: number, dasharray?: string) => ({
    style: { stroke: color, strokeWidth, ...(dasharray ? { strokeDasharray: dasharray } : {}) },
    markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
  })

  // BuiltInEdge：smoothstep 的 pathOptions 只在联合类型的对应分支上（基础 Edge 无此字段）
  const edges: BuiltInEdge[] = graph.edges
    .filter((e) => visible.has(e.src_id) && visible.has(e.dst_id))
    .map((e, idx) => ({
      id: `${e.src_id}-${e.dst_id}-${e.rel}-${idx}`,
      source: e.src_id,
      target: e.dst_id,
      type: 'smoothstep',
      pathOptions: { borderRadius: 14 },
      // 执行中动作的出边流动：图是运行时探索图，活跃工作可感知
      animated: stateById.get(e.src_id) === 'running',
      ...(e.rel === 'triggers'
        ? styled(EDGE_COLORS.triggers, 1.6, '6 4')
        : styled(EDGE_COLORS.generates, 1.6)),
    }))

  // 派生依赖边：前置动作 → 依赖它的动作（去重；引用缺失/自指静默跳过）
  const depSeen = new Set<string>()
  for (const n of graph.nodes) {
    if (!n.depends_on?.length) continue
    for (const depId of n.depends_on) {
      if (depId === n.id || !visible.has(depId) || !visible.has(n.id)) continue
      const key = `${depId}->${n.id}`
      if (depSeen.has(key)) continue
      depSeen.add(key)
      edges.push({
        id: `dep-${key}`,
        source: depId,
        target: n.id,
        type: 'smoothstep',
        pathOptions: { borderRadius: 14 },
        animated: stateById.get(depId) === 'running',
        ...styled(EDGE_COLORS.depends, 1.2, '2 4'),
      })
    }
  }

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
        style: { ...base, stroke: dim(typeof base.stroke === 'string' ? base.stroke : EDGE_COLORS.generates) },
        markerEnd:
          e.markerEnd && typeof e.markerEnd === 'object'
            ? { ...e.markerEnd, color: dim(typeof e.markerEnd.color === 'string' ? e.markerEnd.color : EDGE_COLORS.generates) }
            : e.markerEnd,
      }
    }),
  }
}
