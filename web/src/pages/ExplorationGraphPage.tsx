import { useEffect, useState, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ReactFlow, type Node, type Edge, Background, Controls, MiniMap, useNodesState, useEdgesState, MarkerType, Panel } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'
import { Search, RefreshCw, Filter, Info, AlertCircle } from 'lucide-react'
import { getTaskGraph } from '@/api/explorationgraph'
import { listConversations } from '@/api/client'
import type { GraphNode } from '@/api/explorationgraph'
import { cn } from '@/lib/utils'

/**
 * ExplorationGraphPage - 探索图可视化页面
 *
 * 设计改进：
 * 1. 整合设计 Token（颜色从设计系统获取）
 * 2. 更精致的节点样式（圆角、阴影、渐变）
 * 3. 更清晰的筛选栏
 * 4. 更流畅的加载/错误/空状态
 */

// 节点类型颜色（使用设计系统的色彩）
const NODE_COLORS = {
  objective: '#a78bfa', // violet
  action: '#34d399', // emerald（与 accent 一致）
  observation: '#38bdf8', // sky blue
  result: '#f472b6', // pink
}

// 状态颜色（使用设计系统的语义色）
const STATE_COLORS = {
  open: '#6b7280', // gray
  running: '#3b82f6', // blue
  done: '#10b981', // green（success）
  failed: '#ef4444', // red（error）
  aborted: '#9ca3af', // gray
}

const getLayoutedElements = (nodes: Node[], edges: Edge[], direction = 'TB') => {
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

export function ExplorationGraphPage() {
  const [searchParams] = useSearchParams()
  const [taskId, setTaskId] = useState<string>(searchParams.get('task_id') || '')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [filterKind, setFilterKind] = useState<string>('all')
  const [filterState, setFilterState] = useState<string>('all')
  const [searchText, setSearchText] = useState<string>('')
  const [direction, setDirection] = useState<'TB' | 'LR'>('TB')
  const [stats, setStats] = useState({ total: 0, objectives: 0, actions: 0, observations: 0, results: 0 })
  const [latestTaskInfo, setLatestTaskInfo] = useState<{ title: string; taskId: string } | null>(null)

  useEffect(() => {
    const taskIdFromUrl = searchParams.get('task_id')
    if (taskIdFromUrl && taskIdFromUrl !== taskId) {
      setTaskId(taskIdFromUrl)
    } else if (!taskIdFromUrl && !taskId) {
      loadLatestTask()
    }
  }, [searchParams])

  useEffect(() => {
    if (taskId) {
      loadGraph()
    }
  }, [taskId])

  const loadLatestTask = async () => {
    try {
      const { conversations } = await listConversations(1, 0, '')
      if (conversations.length > 0 && conversations[0].TaskID) {
        setLatestTaskInfo({
          title: conversations[0].Title || '最新对话',
          taskId: conversations[0].TaskID,
        })
        setTaskId(conversations[0].TaskID)
      }
    } catch {
      // 静默失败
    }
  }

  const loadGraph = useCallback(async () => {
    if (!taskId) return
    setLoading(true)
    setError('')
    try {
      const data = await getTaskGraph(taskId)

      const flowNodes: Node[] = data.nodes.map((n: GraphNode) => {
        const baseColor = NODE_COLORS[n.kind as keyof typeof NODE_COLORS] || '#6b7280'
        const stateColor = n.state ? STATE_COLORS[n.state as keyof typeof STATE_COLORS] : null

        let opacity = 1
        if (n.state === 'done') opacity = 0.8
        else if (n.state === 'failed' || n.state === 'aborted') opacity = 0.6

        let label = n.kind.toUpperCase()
        const content = n.content as Record<string, unknown>
        if (content) {
          if (content.description) label += `\n${String(content.description).slice(0, 60)}`
          else if (content.instruction) label += `\n${String(content.instruction).slice(0, 60)}`
          else if (content.title) label += `\n${String(content.title).slice(0, 60)}`
          else if (content.goal) label += `\n${String(content.goal).slice(0, 60)}`
          else if (content.text) label += `\n${String(content.text).slice(0, 60)}`
        }
        if (n.state) label += `\n[${n.state}]`
        if (n.priority) label += ` P${n.priority}`

        return {
          id: n.id,
          type: 'default',
          position: { x: 0, y: 0 },
          data: {
            label: label.length > 100 ? label.slice(0, 100) + '...' : label,
            node: n,
          },
          style: {
            background: `linear-gradient(135deg, ${baseColor} 0%, ${baseColor}dd 100%)`,
            opacity: opacity,
            color: '#fff',
            border: stateColor ? `3px solid ${stateColor}` : '2px solid rgba(255, 255, 255, 0.15)',
            borderRadius: '12px',
            padding: '16px',
            width: 300,
            fontSize: '12px',
            fontWeight: '500',
            whiteSpace: 'pre-wrap',
            lineHeight: '1.5',
            boxShadow: '0 4px 12px rgba(0, 0, 0, 0.3)',
          },
        }
      })

      const flowEdges: Edge[] = data.edges.map((e, idx) => {
        // 不同关系类型使用不同颜色
        const edgeColor = e.rel === 'GENERATES' ? '#3b82f6' :
                         e.rel === 'CONFIRMS' ? '#10b981' :
                         e.rel === 'REFUTES' ? '#ef4444' :
                         e.rel === 'ENABLES' ? '#8b5cf6' :
                         '#6b7280'

        return {
          id: `${e.src_id}-${e.dst_id}-${idx}`,
          source: e.src_id,
          target: e.dst_id,
          type: 'smoothstep',
          animated: false,
          style: {
            stroke: edgeColor,
            strokeWidth: 2,
          },
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: 20,
            height: 20,
            color: edgeColor,
          },
          label: e.rel,
          labelStyle: {
            fill: '#9ca3af',
            fontSize: 10,
          },
        }
      })

      const layouted = getLayoutedElements(flowNodes, flowEdges, direction)
      setNodes(layouted.nodes)
      setEdges(layouted.edges)

      const newStats = {
        total: data.nodes.length,
        objectives: data.nodes.filter((n: GraphNode) => n.kind === 'objective').length,
        actions: data.nodes.filter((n: GraphNode) => n.kind === 'action').length,
        observations: data.nodes.filter((n: GraphNode) => n.kind === 'observation').length,
        results: data.nodes.filter((n: GraphNode) => n.kind === 'result').length,
      }
      setStats(newStats)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
      setNodes([])
      setEdges([])
    } finally {
      setLoading(false)
    }
  }, [taskId, direction, setNodes, setEdges])

  const handleTaskIdSubmit = () => {
    if (taskId) {
      loadGraph()
    }
  }

  const toggleDirection = () => {
    setDirection((d) => (d === 'TB' ? 'LR' : 'TB'))
  }

  useEffect(() => {
    if (nodes.length > 0) {
      const layouted = getLayoutedElements(nodes, edges, direction)
      setNodes(layouted.nodes)
      setEdges(layouted.edges)
    }
  }, [direction])

  const filteredNodes = nodes.filter((node) => {
    const n = node.data.node as GraphNode
    if (filterKind !== 'all' && n.kind !== filterKind) return false
    if (filterState !== 'all' && n.state !== filterState) return false
    if (searchText) {
      const label = node.data.label as string
      return label.toLowerCase().includes(searchText.toLowerCase())
    }
    return true
  })

  const filteredEdges = edges.filter((edge) => {
    return filteredNodes.some((n) => n.id === edge.source) && filteredNodes.some((n) => n.id === edge.target)
  })

  return (
    <div className="flex h-full flex-col bg-background">
      {/* 头部 */}
      <header className="flex items-center justify-between gap-4 border-b border-border bg-surface px-6 py-4">
        <div className="flex flex-col gap-1.5">
          <h1 className="text-base font-semibold text-text">
            探索图
          </h1>
          <p className="text-sm text-text-secondary">
            智能体推理与行动的可视化决策树
          </p>
        </div>
          {stats.total > 0 && (
            <div className="flex items-center gap-3 text-xs text-text-tertiary">
              <span>共 {stats.total} 个节点</span>
              <span>·</span>
              <span>{stats.objectives} 目标</span>
              <span>·</span>
              <span>{stats.actions} 动作</span>
              <span>·</span>
              <span>{stats.observations} 观测</span>
              <span>·</span>
              <span>{stats.results} 结果</span>
            </div>
          )}
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={taskId}
              onChange={(e) => setTaskId(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleTaskIdSubmit()}
              placeholder="输入任务 ID"
              className={cn(
                'h-9 w-80 rounded-lg border border-border bg-surface px-3 text-sm text-text',
                'outline-none transition-all placeholder:text-text-tertiary',
                'focus:border-accent focus:glow-accent disabled:opacity-50'
              )}
              disabled={loading}
            />
            <button
              onClick={handleTaskIdSubmit}
              disabled={loading || !taskId}
              className={cn(
                'flex h-9 items-center gap-2 rounded-lg px-4 text-sm font-semibold',
                'transition-all',
                loading || !taskId
                  ? 'cursor-not-allowed bg-surface-2 text-text-disabled'
                  : 'bg-accent-soft hover:bg-accent/10 text-accent hover:text-accent-hover'
              )}
            >
              <Search className="h-4 w-4" />
              加载
            </button>
          </div>
          {nodes.length > 0 && (
            <button
              onClick={loadGraph}
              disabled={loading}
              title="刷新"
              className={cn(
                'flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-surface',
                'transition-all hover:bg-surface-2 disabled:opacity-50'
              )}
            >
              <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />
            </button>
          )}
        </div>
      </header>

      {/* 筛选栏 */}
      {nodes.length > 0 && (
        <div className="flex items-center gap-4 border-b border-border bg-surface/50 px-6 py-3">
          <div className="flex items-center gap-2">
            <Filter className="h-4 w-4 text-text-tertiary" />
            <span className="text-sm text-text-secondary">筛选:</span>
          </div>
          <select
            value={filterKind}
            onChange={(e) => setFilterKind(e.target.value)}
            className={cn(
              'h-8 rounded-lg border border-border bg-surface px-3 text-xs text-text',
              'outline-none transition-all focus:border-accent focus:glow-accent'
            )}
          >
            <option value="all">全部类型</option>
            <option value="objective">目标</option>
            <option value="action">动作</option>
            <option value="observation">观测</option>
            <option value="result">结果</option>
          </select>
          <select
            value={filterState}
            onChange={(e) => setFilterState(e.target.value)}
            className={cn(
              'h-8 rounded-lg border border-border bg-surface px-3 text-xs text-text',
              'outline-none transition-all focus:border-accent focus:glow-accent'
            )}
          >
            <option value="all">全部状态</option>
            <option value="open">待处理</option>
            <option value="running">执行中</option>
            <option value="done">已完成</option>
            <option value="failed">失败</option>
            <option value="aborted">已中止</option>
          </select>
          <input
            type="text"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            placeholder="搜索节点内容..."
            className={cn(
              'h-8 w-60 rounded-lg border border-border bg-surface px-3 text-xs text-text',
              'outline-none transition-all placeholder:text-text-tertiary',
              'focus:border-accent focus:glow-accent'
            )}
          />
          <div className="ml-auto flex items-center gap-2">
            <button
              onClick={toggleDirection}
              className={cn(
                'h-8 rounded-lg border border-border bg-surface px-3 text-xs',
                'transition-all hover:bg-surface-2'
              )}
            >
              {direction === 'TB' ? '垂直布局' : '水平布局'}
            </button>
          </div>
        </div>
      )}

      {/* 画布区域 */}
      <div className="relative flex-1 bg-[#0a0d12]">
        {loading ? (
          <div className="flex h-full flex-col items-center justify-center gap-4">
            <div className="h-12 w-12 animate-spin rounded-full border-4 border-accent border-t-transparent" />
            <span className="font-mono text-sm text-text-secondary terminal-cursor">加载中</span>
          </div>
        ) : error ? (
          <div className="flex h-full flex-col items-center justify-center gap-4">
            <div className="flex h-16 w-16 items-center justify-center rounded-full bg-status-error/10">
              <AlertCircle className="h-8 w-8 text-status-error" />
            </div>
            <div className="text-lg font-medium text-status-error">{error}</div>
            <div className="max-w-md text-center text-sm text-text-secondary">
              请检查任务 ID 是否正确（需要 UUID 格式），或从对话列表中选择一个任务查看其探索图
            </div>
          </div>
        ) : nodes.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-text-secondary">
            <div className="max-w-md text-center">
              <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-surface-2">
                <Info className="h-8 w-8 text-text-tertiary" />
              </div>
              <div className="mb-2 text-lg font-medium text-text">暂无数据</div>
              <div className="text-sm">
                请输入任务 ID 并点击加载，或通过 URL 参数{' '}
                <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">?task_id=xxx</code> 访问
              </div>
            </div>
          </div>
        ) : (
          <ReactFlow
            nodes={filteredNodes}
            edges={filteredEdges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            fitView
            minZoom={0.1}
            maxZoom={2}
            defaultViewport={{ x: 0, y: 0, zoom: 0.8 }}
          >
            <Background color="#1e293b" gap={20} size={1} />
            <Controls />
            <MiniMap
              nodeColor={(node) => (node.style?.background as string) || '#6b7280'}
              maskColor="rgba(0, 0, 0, 0.6)"
              pannable
              zoomable
            />
            <Panel position="top-right" className="rounded-lg bg-surface/90 p-3 text-xs shadow-lg backdrop-blur-sm">
              <div className="flex flex-col gap-1.5">
                <div className="mb-1 font-semibold text-text">节点统计</div>
                <div className="text-text-secondary">
                  显示: <span className="font-mono font-semibold text-text">{filteredNodes.length}</span> /{' '}
                  {nodes.length}
                </div>
                <div className="text-text-secondary">
                  边: <span className="font-mono font-semibold text-text">{filteredEdges.length}</span> / {edges.length}
                </div>
              </div>
            </Panel>
          </ReactFlow>
        )}
      </div>

      {/* 图例 */}
      <div className="flex gap-8 border-t border-border bg-surface px-6 py-3 text-xs">
        <div className="flex items-center gap-4">
          <span className="font-semibold text-text-secondary">节点类型:</span>
          {Object.entries(NODE_COLORS).map(([kind, color]) => (
            <div key={kind} className="flex items-center gap-2">
              <div className="h-3 w-3 rounded shadow-sm" style={{ background: color }} />
              <span className="capitalize text-text">{kind}</span>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-4">
          <span className="font-semibold text-text-secondary">状态:</span>
          {Object.entries(STATE_COLORS).map(([state, color]) => (
            <div key={state} className="flex items-center gap-2">
              <div className="h-3 w-3 rounded shadow-sm" style={{ background: color }} />
              <span className="capitalize text-text">{state}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
