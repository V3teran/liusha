import dagre from '@dagrejs/dagre'
import type { Edge, Node } from '@xyflow/react'

// 探索图视觉与布局常量（页面组件只消费，不再持 dagre 细节）。

export const NODE_COLORS = {
  objective: '#a78bfa', // violet
  action: '#34d399', // emerald（与 accent 一致）
  observation: '#38bdf8', // sky blue
  result: '#f472b6', // pink
}

// 状态颜色（使用设计系统的语义色）
export const STATE_COLORS = {
  open: '#6b7280', // gray
  running: '#3b82f6', // blue
  completed: '#10b981', // green（success）
  exhausted: '#f59e0b', // amber（预算耗尽）
  failed: '#ef4444', // red（error）
  aborted: '#9ca3af', // gray
}

export const getLayoutedElements = (nodes: Node[], edges: Edge[], direction = 'TB') => {
  const dagreGraph = new dagre.graphlib.Graph()
  dagreGraph.setDefaultEdgeLabel(() => ({}))
  dagreGraph.setGraph({ rankdir: direction, nodesep: 100, ranksep: 150 })

  nodes.forEach((node) => {
    dagreGraph.setNode(node.id, { width: 300, height: 120 })
  })

  edges.forEach((edge) => {
    dagreGraph.setEdge(edge.source, edge.target)
  })

  dagre.layout(dagreGraph)

  const layoutedNodes = nodes.map((node) => {
    const nodeWithPosition = dagreGraph.node(node.id)
    return {
      ...node,
      position: {
        x: nodeWithPosition.x - 150,
        y: nodeWithPosition.y - 60,
      },
    }
  })

  return { nodes: layoutedNodes, edges }
}
