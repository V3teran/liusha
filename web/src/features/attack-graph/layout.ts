import dagre from 'dagre'
import type { GraphEdgeData, GraphNodeData } from './graphTransform'

export interface PositionedNode {
  id: string
  data: GraphNodeData
  x: number
  y: number
  width: number
  height: number
}

// 节点尺寸：漏洞星形最大（攻击链终点，视觉锚），target 次之（交战根），
// 其余 3 类语义节点（asset/credential/access）统一尺寸——形状+颜色已分维度，无需再靠大小区分。
export function nodeSize(data: GraphNodeData): number {
  if (data.kind === 'finding') return 44
  if (data.kind === 'target') return 36
  return 26
}

// 标签最多显示的行数（与 nodes.tsx 的 line-clamp-2 对齐，二者必须一致：
// 这里声明的布局高度决定 dagre 给相邻层留多少间距，nodes.tsx 决定标签实际截断到几行——
// 只声明"最多两行"却渲染成不限行数会溢出层间距，反过来只截断成一行却按两行留间距则白白
// 浪费空间，两边刻意不各自定义常量而是共享同一个，防止将来改一边忘了改另一边）。
export const LABEL_MAX_LINES = 2
// 单行标签的像素高度（对齐 nodes.tsx 里 text-[11px] leading-snug 的实际行高）。
const LABEL_LINE_HEIGHT = 15

/**
 * layoutGraph 用 dagre 计算自上而下（TB）分层布局，对齐原 G6 antv-dagre 配置
 * （rankdir: TB, nodesep: 18, ranksep: 28）。
 *
 * 节点声明高度必须覆盖标签最多可能占用的行数（LABEL_MAX_LINES），否则长标签换行后
 * 会溢出 dagre 分配的层间距，视觉上盖住同层或下一层节点——旧实现按固定单行高度
 * （size + 8）声明，但 nodes.tsx 的标签是 whitespace-normal 可换行，二者不匹配。
 */
export function layoutGraph(nodes: GraphNodeData[], edges: GraphEdgeData[]): PositionedNode[] {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', nodesep: 18, ranksep: 28 })
  g.setDefaultEdgeLabel(() => ({}))

  for (const n of nodes) {
    const size = nodeSize(n)
    // 节点宽度留出标签空间（标签在右侧显示，dagre 按此尺寸分层排布，需比纯圆点宽）。
    // 高度覆盖最多 LABEL_MAX_LINES 行标签，不再假设标签只占一行。
    g.setNode(n.id, { width: size + 140, height: Math.max(size + 8, LABEL_MAX_LINES * LABEL_LINE_HEIGHT) })
  }
  for (const e of edges) {
    if (g.hasNode(e.source) && g.hasNode(e.target)) g.setEdge(e.source, e.target)
  }

  dagre.layout(g)

  return nodes.map((n) => {
    const pos = g.node(n.id)
    const size = nodeSize(n)
    return {
      id: n.id,
      data: n,
      x: pos?.x ?? 0,
      y: pos?.y ?? 0,
      width: size,
      height: size,
    }
  })
}
