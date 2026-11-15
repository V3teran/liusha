import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { AlertCircle, Info } from 'lucide-react'
import { useLatestTaskId, useTaskGraph, useTaskVerifications } from '@/features/exploration/useExplorationQueries'
import { applyFocus, buildFlowGraph, type GraphStats } from '@/features/exploration/layout'
import { GraphCanvas } from '@/features/exploration/GraphCanvas'
import { Toolbar } from '@/features/exploration/Toolbar'
import { NodeDetailPanel } from '@/features/exploration/NodeDetailPanel'
import type { Verification } from '@/api/explorationgraph'
import { useConversation } from '@/features/conversation/useConversationQueries'
import { scopeOf } from '@/features/relations/scope'
import { useScopeLabel } from '@/features/relations/useScopeLabel'
import { TaskJumpLinks } from '@/features/relations/TaskJumpLinks'

const EMPTY_STATS: GraphStats = { total: 0, edgeCount: 0, byKind: {} }

/**
 * ExplorationGraphPage - 探索图可视化页面（薄组装层）。
 * 数据取数在 useExplorationQueries，图构建/布局/聚焦在 layout.ts，
 * 画布渲染在 GraphCanvas。探索图属于任务；范围以 URL 为单一事实源：?task= 是这个任务的图，
 * ?conversation= 是这个对话的图（从对话跳来，显示对话标题；图按对话所属的任务取）。
 */
export function ExplorationGraphPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const scope = scopeOf({ task: searchParams.get('task') ?? '', conversation: searchParams.get('conversation') ?? '' })
  const conversationQuery = useConversation(scope?.kind === 'conversation' ? scope.id : '')
  const taskId = scope?.kind === 'task' ? scope.id : (conversationQuery.data?.task_id ?? '')
  const scopeLabel = useScopeLabel(scope)
  // 仅挂载时缺范围才自动解析最新任务：用户清空选择后不应再抢跑回填
  const shouldResolveLatest = useRef(!scope).current

  const latestQuery = useLatestTaskId(shouldResolveLatest)
  useEffect(() => {
    const id = latestQuery.data
    if (!shouldResolveLatest || scope || !id) return
    setSearchParams(
      (prev) => {
        if (prev.get('task') || prev.get('conversation')) return prev
        const p = new URLSearchParams(prev)
        p.set('task', id)
        return p
      },
      { replace: true },
    )
  }, [latestQuery.data, scope, shouldResolveLatest, setSearchParams])

  const graphQuery = useTaskGraph(taskId)
  const verifQuery = useTaskVerifications(taskId)
  const graph = graphQuery.data

  // node_id → 最新一条判定记录（时间倒序，首条即最新）
  const verificationsByNode = useMemo(() => {
    const byNode = new Map<string, Verification>()
    for (const v of verifQuery.data?.verifications ?? []) {
      if (!byNode.has(v.node_id)) byNode.set(v.node_id, v)
    }
    return byNode
  }, [verifQuery.data])

  const [filterKind, setFilterKind] = useState('all')
  const [filterState, setFilterState] = useState('all')
  const [selectedNodeId, setSelectedNodeId] = useState('')
  useEffect(() => setSelectedNodeId(''), [taskId])

  const selectTask = (id: string) => setSearchParams(id ? { task: id } : {}, { replace: true })

  const flow = useMemo(
    () => (graph ? buildFlowGraph(graph, filterKind, filterState, verificationsByNode) : { nodes: [], edges: [], stats: EMPTY_STATS }),
    [graph, filterKind, filterState, verificationsByNode],
  )
  const view = useMemo(() => applyFocus(flow, selectedNodeId), [flow, selectedNodeId])

  const refresh = () => {
    if (!taskId) return
    void graphQuery.refetch()
    void verifQuery.refetch()
  }

  const resolvingLatest = shouldResolveLatest && !scope && latestQuery.isPending
  const loading = (scope?.kind === 'conversation' && conversationQuery.isPending) || (taskId !== '' && graphQuery.isPending)
  const failed = conversationQuery.isError ? conversationQuery.error : taskId !== '' && graphQuery.isError ? graphQuery.error : null
  const error = failed ? (failed instanceof Error ? failed.message : '加载失败') : ''
  // 从纯聊天对话进来：没有任务，也就没有探索图
  const chatOnly = scope?.kind === 'conversation' && conversationQuery.isSuccess && !taskId
  const hasGraph = (graph?.nodes.length ?? 0) > 0
  const selectedNode = selectedNodeId ? graph?.nodes.find((n) => n.id === selectedNodeId) : undefined

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader title="探索图" subtitle="每个任务一张图：智能体的目标、行动与结论如何达成">
        {taskId ? <TaskJumpLinks taskId={taskId} targets={['task', 'conversation', 'findings']} /> : null}
      </PageHeader>

      <Toolbar
        scopeId={scope?.id ?? ''}
        scopeLabel={scopeLabel}
        onTaskChange={selectTask}
        disabled={loading}
        refreshing={graphQuery.isFetching}
        onRefresh={refresh}
        showFilters={hasGraph}
        filterKind={filterKind}
        onFilterKind={setFilterKind}
        filterState={filterState}
        onFilterState={setFilterState}
        stats={flow.stats}
      />

      {/* 画布区域 */}
      <div className="relative flex-1 bg-background">
        {resolvingLatest || loading ? (
          <div className="flex h-full flex-col items-center justify-center gap-4">
            <div className="h-12 w-12 animate-spin rounded-full border-4 border-accent border-t-transparent" />
            <span className="font-mono text-sm text-text-secondary terminal-cursor">
              {resolvingLatest ? '定位最新任务' : '加载中'}
            </span>
          </div>
        ) : error ? (
          <div className="flex h-full flex-col items-center justify-center gap-4">
            <div className="flex h-16 w-16 items-center justify-center rounded-full bg-status-error/10">
              <AlertCircle className="h-8 w-8 text-status-error" />
            </div>
            <div className="text-lg font-medium text-status-error">{error}</div>
            <div className="max-w-md text-center text-sm text-text-secondary">
              {scope?.kind === 'conversation' ? '对话' : '任务'}不存在或已删除，请在上方重新选择任务
            </div>
          </div>
        ) : !hasGraph ? (
          <div className="flex h-full flex-col items-center justify-center text-text-secondary">
            <div className="max-w-md text-center">
              <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-surface-2">
                <Info className="h-8 w-8 text-text-tertiary" />
              </div>
              <div className="mb-2 text-lg font-medium text-text">暂无数据</div>
              <div className="text-sm">
                {chatOnly
                  ? '这个对话是纯聊天，没有任务，也就没有探索图'
                  : taskId
                    ? '这个任务还没有探索图（尚未开始规划）'
                    : '在上方选择一个任务，查看它的探索图'}
              </div>
            </div>
          </div>
        ) : (
          <GraphCanvas nodes={view.nodes} edges={view.edges} resetKey={taskId} onNodeClick={setSelectedNodeId} />
        )}
        {selectedNode && (
          <NodeDetailPanel
            node={selectedNode}
            verification={verificationsByNode.get(selectedNode.id)}
            onClose={() => setSelectedNodeId('')}
          />
        )}
      </div>
    </div>
  )
}
