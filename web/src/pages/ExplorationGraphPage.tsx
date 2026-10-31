import { useEffect, useState, useCallback, useRef } from 'react'
import { getLayoutedElements, NODE_COLORS, STATE_COLORS } from '@/lib/graphLayout'
import { useSearchParams } from 'react-router-dom'
import { ReactFlow, type Node, type Edge, Background, Controls, MiniMap, useNodesState, useEdgesState, MarkerType } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Search, RefreshCw, Info, AlertCircle, X } from 'lucide-react'
import { getConversationGraph, getConversationVerifications } from '@/api/explorationgraph'
import { listConversations } from '@/api/conversations'
import type { GraphNode, Verification } from '@/api/explorationgraph'
import { cn } from '@/lib/utils'

/**
 * ExplorationGraphPage - 探索图可视化页面
 */

// 节点类型颜色（使用设计系统的色彩）
export function ExplorationGraphPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  // conversationId 以 URL 为单一事实源（挂载时缺省回填最新会话，replace 写回）
  const conversationId = searchParams.get('conversation_id') || ''
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [filterKind, setFilterKind] = useState<string>('all')
  const [filterState, setFilterState] = useState<string>('all')
  const [direction, setDirection] = useState<'TB' | 'LR'>('TB')
  const [stats, setStats] = useState({ total: 0, objectives: 0, actions: 0, observations: 0, results: 0 })
  // 节点详情：点击节点查看内容与验证审计链（node_id → 最新一条裁决记录）
  const [selectedNodeId, setSelectedNodeId] = useState<string>('')
  const [verificationsByNode, setVerificationsByNode] = useState<Map<string, Verification>>(new Map())

  useEffect(() => {
    if (conversationId) {
      loadGraph()
    }
    // 只随会话切换重拉；direction 变化走下方重排 effect，不重新取数。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversationId])

  // 默认展示最新会话：URL 未带 conversation_id 时取最近一个会话（updated_at DESC 首条），
  // 并回写 URL（replace）——刷新/分享链接都能还原同一张图。
  useEffect(() => {
    if (searchParams.get('conversation_id')) return
    let cancelled = false
    listConversations(1, 0, '')
      .then(({ conversations }) => {
        const latest = conversations[0]?.id
        if (!latest || cancelled) return
        setSearchParams(
          (prev) => {
            if (prev.get('conversation_id')) return prev
            const p = new URLSearchParams(prev)
            p.set('conversation_id', latest)
            return p
          },
          { replace: true },
        )
      })
      .catch(() => {
        // 静默：无会话时保持空态，可手动输入对话 ID
      })
    return () => {
      cancelled = true
    }
    // 仅挂载时判定一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const seqRef = useRef(0)
  const loadGraph = useCallback(async () => {
    if (!conversationId) return
    const mySeq = ++seqRef.current
    setLoading(true)
    setError('')
    try {
      const data = await getConversationGraph(conversationId)
      if (mySeq !== seqRef.current) return // 已切换会话：丢弃过期响应

      const flowNodes: Node[] = data.nodes.map((n: GraphNode) => {
        const baseColor = NODE_COLORS[n.kind as keyof typeof NODE_COLORS] || '#6b7280'
        const stateColor = n.state ? STATE_COLORS[n.state as keyof typeof STATE_COLORS] : null

        let opacity = 1
        if (n.state === 'completed') opacity = 0.8
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
        const edgeColor = e.rel === 'generates' ? '#3b82f6' :
                         e.rel === 'triggers' ? '#10b981' :
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

      // 验证审计链 best-effort 并行拉取：接口失败不阻塞图渲染
      setSelectedNodeId('')
      getConversationVerifications(conversationId)
        .then(({ verifications }) => {
          if (mySeq !== seqRef.current) return
          const byNode = new Map<string, Verification>()
          for (const v of verifications) {
            if (!byNode.has(v.node_id)) byNode.set(v.node_id, v) // 时间倒序，首条即最新
          }
          setVerificationsByNode(byNode)
        })
        .catch(() => setVerificationsByNode(new Map()))
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
      setNodes([])
      setEdges([])
    } finally {
      setLoading(false)
    }
  }, [conversationId, direction, setNodes, setEdges])

  const handleConversationIdSubmit = () => {
    if (conversationId) {
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
    // 闭包中的 nodes/edges 恒为本次渲染最新值（effect 在渲染后执行）；
    // 只随 direction 翻转重排，数据更新由 loadGraph 自带布局。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [direction])

  const filteredNodes = nodes.filter((node) => {
    const n = node.data.node as GraphNode
    if (filterKind !== 'all' && n.kind !== filterKind) return false
    if (filterState !== 'all' && n.state !== filterState) return false
    return true
  })

  const filteredEdges = edges.filter((edge) => {
    return filteredNodes.some((n) => n.id === edge.source) && filteredNodes.some((n) => n.id === edge.target)
  })

  const selectedNode = selectedNodeId
    ? (nodes.find((n) => n.id === selectedNodeId)?.data.node as GraphNode | undefined)
    : undefined

  return (
    <div className="flex h-full flex-col bg-background">
      {/* 头部 */}
      <header className="flex items-center justify-between gap-4 border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">探索图</h1>
          <p className="mt-0.5 text-sm text-text-secondary">智能体推理与行动的可视化决策树</p>
        </div>
      </header>

      {/* 筛选栏 */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-surface/50 px-6 py-3">
        {/* 会话搜索框：回车即加载（无独立加载按钮）；常驻——空态时的唯一入口 */}
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <input
            type="text"
            value={conversationId}
            onChange={(e) => setSearchParams(e.target.value ? { conversation_id: e.target.value } : {}, { replace: true })}
            onKeyDown={(e) => e.key === 'Enter' && handleConversationIdSubmit()}
            placeholder="输入对话 ID，回车加载"
            className={cn(
              'h-8 w-72 rounded-lg border border-border bg-surface pl-9 pr-3 text-xs text-text',
              'outline-none transition-all placeholder:text-text-tertiary',
              'focus:border-accent focus:glow-accent disabled:opacity-50'
            )}
            disabled={loading}
          />
        </div>
        {nodes.length > 0 && (
          <button
            onClick={loadGraph}
            disabled={loading}
            title="刷新"
            className={cn(
              'flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border border-border bg-surface',
              'transition-all hover:bg-surface-2 disabled:opacity-50'
            )}
          >
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          </button>
        )}
        {nodes.length > 0 && (
          <>
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
            <option value="completed">已完成</option>
            <option value="exhausted">预算耗尽</option>
            <option value="failed">失败</option>
            <option value="aborted">已中止</option>
          </select>
          {stats.total > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              {/* 总数徽章：accent 描边突出 */}
              <span
                title="节点总数"
                className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-accent/40 bg-accent/10 px-2.5 text-xs font-semibold text-accent"
              >
                {stats.total} 节点
              </span>
              <span
                title="连线总数"
                className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-accent/40 bg-accent/10 px-2.5 text-xs font-semibold text-accent"
              >
                {edges.length} 边
              </span>
              {/* 各类型徽章：节点色点 + 数量（NODE_COLORS 同源） */}
              {(
                [
                  { key: 'objective', label: '目标', count: stats.objectives },
                  { key: 'action', label: '动作', count: stats.actions },
                  { key: 'observation', label: '观测', count: stats.observations },
                  { key: 'result', label: '结果', count: stats.results },
                ] as const
              ).map(({ key, label, count }) => (
                <span
                  key={key}
                  title={`${label}节点数`}
                  className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-border bg-surface px-2.5 text-xs text-text"
                >
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ background: NODE_COLORS[key as keyof typeof NODE_COLORS] }}
                  />
                  {count} {label}
                </span>
              ))}
            </div>
          )}
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
          </>
        )}
      </div>


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
                请输入对话 ID 并点击加载，或通过 URL 参数{' '}
                <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">?conversation_id=xxx</code> 访问
              </div>
            </div>
          </div>
        ) : (
          <ReactFlow
            nodes={filteredNodes}
            edges={filteredEdges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={(_, node) => setSelectedNodeId(node.id)}
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
          </ReactFlow>
        )}
        {selectedNode && (
          <NodeDetailPanel
            node={selectedNode}
            verification={verificationsByNode.get(selectedNode.id)}
            onClose={() => setSelectedNodeId('')}
          />
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

// NodeDetailPanel 节点详情浮层：内容字段 + 该节点的最新验证审计记录
// （坐实/证伪的机器证据——审计链的可读出口）。
function NodeDetailPanel({
  node,
  verification,
  onClose,
}: {
  node: GraphNode
  verification?: Verification
  onClose: () => void
}) {
  const content = (node.content ?? {}) as Record<string, unknown>
  const contentEntries = Object.entries(content).filter(([, v]) => v != null && v !== '')
  const outcomeLabel = verification?.outcome === 'confirmed' ? '已坐实' : '已证伪'

  return (
    <div className="absolute right-4 top-4 z-10 flex max-h-[calc(100%-2rem)] w-96 flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-2xl">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <div className="flex items-center gap-2">
          <span
            className="h-2.5 w-2.5 rounded-full"
            style={{ background: NODE_COLORS[node.kind as keyof typeof NODE_COLORS] }}
          />
          <span className="text-sm font-semibold uppercase text-text">{node.kind}</span>
          {node.state && (
            <span className="rounded bg-surface-2 px-1.5 py-0.5 text-xs text-text-secondary">{node.state}</span>
          )}
          {node.priority && (
            <span className="rounded bg-surface-2 px-1.5 py-0.5 text-xs text-text-secondary">P{node.priority}</span>
          )}
        </div>
        <button
          onClick={onClose}
          className="rounded p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
          title="关闭"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-3">
        <section>
          <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary">内容</h4>
          {contentEntries.length === 0 ? (
            <p className="text-xs text-text-tertiary">（空）</p>
          ) : (
            <dl className="space-y-2">
              {contentEntries.map(([k, v]) => (
                <div key={k}>
                  <dt className="text-xs text-text-tertiary">{k}</dt>
                  <dd className="whitespace-pre-wrap break-words text-xs text-text">
                    {typeof v === 'object' ? JSON.stringify(v, null, 2) : String(v)}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </section>

        {verification && (
          <section>
            <h4 className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary">
              验证审计链
              <span
                className={cn(
                  'rounded px-1.5 py-0.5 text-[10px] font-semibold',
                  verification.outcome === 'confirmed'
                    ? 'bg-status-success/15 text-status-success'
                    : 'bg-status-error/15 text-status-error',
                )}
              >
                {outcomeLabel}
              </span>
            </h4>
            <div className="space-y-1 text-xs text-text-secondary">
              <div>
                裁决时刻：<span className="font-mono">{new Date(verification.created_at).toLocaleString()}</span>
              </div>
              {verification.duration_ms > 0 && <div>复现耗时：{verification.duration_ms} ms</div>}
            </div>
            {verification.evaluation != null && (
              <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-2 font-mono text-[10px] leading-relaxed text-text">
                {typeof verification.evaluation === 'string'
                  ? verification.evaluation
                  : JSON.stringify(verification.evaluation, null, 2)}
              </pre>
            )}
          </section>
        )}
      </div>
    </div>
  )
}
