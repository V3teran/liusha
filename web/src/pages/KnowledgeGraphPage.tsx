import { useEffect, useState } from 'react'
import { ReactFlow, type Node, type Edge, Background, Controls, MiniMap, useNodesState, useEdgesState } from '@xyflow/react'
import '@xyflow/react/dist/style.css'

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
  evaluation: '#f59e0b',
  result: '#ef4444',
}

const STATE_COLORS = {
  open: '#6b7280',
  running: '#3b82f6',
  done: '#10b981',
  failed: '#ef4444',
  aborted: '#9ca3af',
}

export function KnowledgeGraphPage() {
  const [tasks, setTasks] = useState<Array<{ task_id: string; node_count: number }>>([])
  const [selectedTask, setSelectedTask] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  useEffect(() => {
    loadTasks()
  }, [])

  useEffect(() => {
    if (selectedTask) {
      loadGraph(selectedTask)
    }
  }, [selectedTask])

  const loadTasks = async () => {
    try {
      const resp = await fetch('http://localhost:8001/api/v1/debug/tasks-with-nodes')
      const data = await resp.json()
      if (data.tasks && data.tasks.length > 0) {
        setTasks(data.tasks)
        setSelectedTask(data.tasks[0].task_id)
      }
    } catch (err) {
      console.error('加载任务列表失败:', err)
    }
  }

  const loadGraph = async (taskId: string) => {
    setLoading(true)
    try {
      const resp = await fetch(`http://localhost:8001/api/v1/tasks/${taskId}/graph`)
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
        type: 'smoothstep',
        animated: true,
        style: { stroke: '#94a3b8', strokeWidth: 2 },
        labelStyle: { fill: '#64748b', fontSize: 10 },
        labelBgStyle: { fill: '#0f172a', fillOpacity: 0.8 },
      }))

      setNodes(flowNodes)
      setEdges(flowEdges)
    } catch (err) {
      console.error('加载图谱失败:', err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex h-full flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border px-6">
        <h1 className="text-lg font-semibold">知识图谱</h1>
        <div className="flex items-center gap-4">
          <select
            value={selectedTask}
            onChange={(e) => setSelectedTask(e.target.value)}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm"
            disabled={loading}
          >
            {tasks.map((t) => (
              <option key={t.task_id} value={t.task_id}>
                {t.task_id.slice(0, 8)}... ({t.node_count} 节点)
              </option>
            ))}
          </select>
        </div>
      </header>

      <div className="flex-1" style={{ background: '#0a0d12' }}>
        {loading ? (
          <div className="flex h-full items-center justify-center text-muted">
            加载中...
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
