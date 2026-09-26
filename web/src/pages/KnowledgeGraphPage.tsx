import { useEffect, useState } from 'react'
import { ReactFlow, type Node, type Edge, Background, Controls, MiniMap, useNodesState, useEdgesState, MarkerType } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'

interface GraphNode {
  id: string
  task_id: string
  kind: string
  content: Record<string, unknown>
  state?: string
  priority?: string
  complexity?: string
  source_type?: string
  created_at: string
}

interface GraphEdge {
  id: string
  from_id: string
  to_id: string
  rel_type: string
}

interface GraphResponse {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

const NODE_COLORS = {
  objective: '#3b82f6',
  action: '#8b5cf6',
  observation: '#10b981',
  result: '#ef4444',
}

const STATE_COLORS = {
  open: '#6b7280',
  running: '#3b82f6',
  done: '#10b981',
  failed: '#ef4444',
  aborted: '#9ca3af',
}

// Dagre layout for hierarchical graph
const getLayoutedElements = (nodes: Node[], edges: Edge[], direction = 'LR') => {
  const dagreGraph = new dagre.graphlib.Graph()
  dagreGraph.setDefaultEdgeLabel(() => ({}))
  dagreGraph.setGraph({ rankdir: direction, nodesep: 100, ranksep: 150 })

  nodes.forEach((node) => {
    dagreGraph.setNode(node.id, { width: 250, height: 100 })
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
        x: nodeWithPosition.x - 125,
        y: nodeWithPosition.y - 50,
      },
    }
  })

  return { nodes: layoutedNodes, edges }
}

export function KnowledgeGraphPage() {
  const [taskId, setTaskId] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  useEffect(() => {
    // 从 URL 参数获取 task_id
    const params = new URLSearchParams(window.location.search)
    const taskIdFromUrl = params.get('task_id')
    if (taskIdFromUrl) {
      setTaskId(taskIdFromUrl)
    }
  }, [])

  useEffect(() => {
    if (taskId) {
      loadGraph(taskId)
    }
  }, [taskId])

  const loadGraph = async (taskId: string) => {
    setLoading(true)
    setError('')
    try {
      const resp = await fetch(`/api/v1/tasks/${taskId}/graph`, {
        headers: { 'X-API-Key': localStorage.getItem('liusha_api_key') || '' }
      })
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`)
      }
      const data: GraphResponse = await resp.json()

      const flowNodes: Node[] = data.nodes.map((n, idx) => {
        const color = n.state ? STATE_COLORS[n.state as keyof typeof STATE_COLORS] : NODE_COLORS[n.kind as keyof typeof NODE_COLORS]

        let label = n.kind.toUpperCase()
        if (n.content) {
          if (n.content.description) label += `\n${n.content.description}`
          else if (n.content.instruction) label += `\n${n.content.instruction}`
          else if (n.content.title) label += `\n${n.content.title}`
        }
        if (n.state) label += `\n[${n.state}]`

        return {
          id: n.id,
          type: 'default',
          position: { x: (idx % 3) * 300, y: Math.floor(idx / 3) * 150 },
          data: {
            label: label.slice(0, 100) + (label.length > 100 ? '...' : '')
          },
          style: {
            background: color,
            color: '#fff',
            border: '2px solid #fff',
            borderRadius: '8px',
            padding: '12px',
            width: 250,
            fontSize: '11px',
            whiteSpace: 'pre-wrap',
          },
        }
      })

      const flowEdges: Edge[] = data.edges.map((e) => ({
        id: e.id,
        source: e.from_id,
        target: e.to_id,
        label: e.rel_type,
        type: 'default',
        animated: false,
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 24,
          height: 24,
          color: '#60a5fa',
        },
        style: {
          stroke: '#60a5fa',
          strokeWidth: 2.5,
        },
        labelStyle: { fill: '#e2e8f0', fontSize: 11, fontWeight: 500 },
        labelBgStyle: { fill: '#1e293b', fillOpacity: 0.95, rx: 4, ry: 4 },
      }))

      const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(flowNodes, flowEdges, 'LR')
      setNodes(layoutedNodes)
      setEdges(layoutedEdges)
    } catch (err) {
      console.error('加载图谱失败:', err)
      setError(err instanceof Error ? err.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex h-full flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border px-6">
        <h1 className="text-lg font-semibold">探索图</h1>
        <div className="flex items-center gap-4">
          <input
            type="text"
            value={taskId}
            onChange={(e) => setTaskId(e.target.value)}
            placeholder="输入任务 ID"
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm w-80"
            disabled={loading}
          />
          <button
            onClick={() => loadGraph(taskId)}
            disabled={loading || !taskId}
            className="rounded-md bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
          >
            加载
          </button>
        </div>
      </header>

      <div className="flex-1" style={{ background: '#0a0d12' }}>
        {loading ? (
          <div className="flex h-full items-center justify-center text-muted">
            加载中...
          </div>
        ) : error ? (
          <div className="flex h-full items-center justify-center flex-col gap-4">
            <div className="text-red-400">{error}</div>
            <div className="text-sm text-muted">请检查任务 ID 是否正确，或在对话列表中选择一个任务</div>
          </div>
        ) : nodes.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted">
            <div className="text-center">
              <div className="mb-2">暂无数据</div>
              <div className="text-sm">请输入任务 ID 或通过 URL 参数 ?task_id=xxx 访问</div>
            </div>
          </div>
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            fitView
            minZoom={0.1}
            maxZoom={2}
          >
            <Background color="#1e293b" gap={16} />
            <Controls />
            <MiniMap
              nodeColor={(node) => node.style?.background as string || '#6b7280'}
              maskColor="rgba(0, 0, 0, 0.6)"
            />
          </ReactFlow>
        )}
      </div>

      <div className="flex gap-6 border-t border-border bg-surface px-6 py-3 text-xs">
        <div className="flex items-center gap-4">
          <span className="font-medium text-muted">节点类型:</span>
          {Object.entries(NODE_COLORS).map(([kind, color]) => (
            <div key={kind} className="flex items-center gap-1.5">
              <div className="h-3 w-3 rounded" style={{ background: color }} />
              <span className="text-text">{kind}</span>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-4">
          <span className="font-medium text-muted">状态:</span>
          {Object.entries(STATE_COLORS).map(([state, color]) => (
            <div key={state} className="flex items-center gap-1.5">
              <div className="h-3 w-3 rounded" style={{ background: color }} />
              <span className="text-text">{state}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
