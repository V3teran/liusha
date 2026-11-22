import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { EmptyState } from '@/components/ui/EmptyState'
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
import { useSelectedParam } from '@/hooks/useSelectedParam'
import { ErrorState } from '@/components/ui/ErrorState'
import { LoadingState } from '@/components/ui/LoadingState'

const EMPTY_STATS: GraphStats = { total: 0, edgeCount: 0, byKind: {} }

/**
 * ExplorationGraphPage - 探索图可视化页面（薄组装层）。
 * 数据取数在 useExplorationQueries，图构建/布局/聚焦在 layout.ts，
 * 画布渲染在 GraphCanvas。探索图属于任务；范围以 URL 为单一事实源：?task= 是这个任务的图，
 * ?conversation= 是这个对话的图（从对话跳来，显示对话标题；图按对话所属的任务取）。
 * 打开的节点是 ?selected=（与列表页打开的条目同一套历史规则）：从发现跳来时即坐实它的结果节点，
 * 画布首屏对准它；换选任务清掉它。
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
  const [selectedNodeId, selectNode] = useSelectedParam()

  // 换选任务是一步导航：新增一条历史，后退回到原来的任务（进页自动定位最新任务才用 replace）；
  // 只留新任务，打开的节点属于原来的图，随之清掉
  const selectTask = (id: string) => setSearchParams(id ? { task: id } : {})

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
  // 进页定位最新任务却一个任务都没有：是「暂无任务」，不是「未选择」
  const noTasks = !scope && shouldResolveLatest && latestQuery.isSuccess && !latestQuery.data
  const selectedNode = selectedNodeId ? graph?.nodes.find((n) => n.id === selectedNodeId) : undefined

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader title="探索图" />

      <Toolbar
        scopeId={scope?.id ?? ''}
        scopeLabel={scopeLabel}
        onTaskChange={selectTask}
        scopeJumps={
          taskId ? <TaskJumpLinks taskId={taskId} size="sm" targets={['task', 'conversation', 'findings']} /> : null
        }
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
          <LoadingState label={resolvingLatest ? '定位最新任务…' : '加载中…'} className="h-full" />
        ) : error ? (
          <ErrorState
            title={error}
            message={`${scope?.kind === 'conversation' ? '对话' : '任务'}不存在或已删除，请重新选择任务。`}
            className="h-full"
          />
        ) : chatOnly ? (
          <EmptyState kind="empty" noun="探索图" hint="这个对话是纯聊天，没有任务。" className="h-full" />
        ) : taskId && !hasGraph ? (
          <EmptyState kind="empty" noun="探索图" hint="任务开始规划后，探索图会出现在这里。" className="h-full" />
        ) : noTasks ? (
          <EmptyState kind="empty" noun="任务" hint="在「任务」页新建任务后，可在这里查看它的探索图。" className="h-full" />
        ) : !taskId ? (
          <EmptyState kind="unselected" noun="任务" hint="选择一个任务，查看它的目标、动作与结论。" className="h-full" />
        ) : (
          <GraphCanvas
            nodes={view.nodes}
            edges={view.edges}
            resetKey={taskId}
            focusNodeId={selectedNode?.id ?? ''}
            onNodeClick={selectNode}
          />
        )}
        {selectedNode && (
          <NodeDetailPanel
            node={selectedNode}
            verification={verificationsByNode.get(selectedNode.id)}
            onClose={() => selectNode('')}
          />
        )}
      </div>
    </div>
  )
}
