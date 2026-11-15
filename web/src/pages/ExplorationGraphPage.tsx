import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { AlertCircle, Info } from 'lucide-react'
import { useConversationGraph, useConversationVerifications, useLatestConversationId } from '@/features/exploration/useExplorationQueries'
import { applyFocus, buildFlowGraph, type GraphStats } from '@/features/exploration/layout'
import { GraphCanvas } from '@/features/exploration/GraphCanvas'
import { Toolbar } from '@/features/exploration/Toolbar'
import { NodeDetailPanel } from '@/features/exploration/NodeDetailPanel'
import type { Verification } from '@/api/explorationgraph'
import { useQuery } from '@tanstack/react-query'
import { getConversation } from '@/api/conversations'

const EMPTY_STATS: GraphStats = { total: 0, edgeCount: 0, byKind: {} }

/**
 * ExplorationGraphPage - 探索图可视化页面（薄组装层）。
 * 数据取数在 useExplorationQueries，图构建/布局/聚焦在 layout.ts，
 * 画布渲染在 GraphCanvas；conversationId 以 URL 为单一事实源。
 */
export function ExplorationGraphPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const conversationId = searchParams.get('conversation_id') || ''
  // 仅挂载时缺 id 才自动解析最新会话：用户清空输入框后不应再抢跑回填
  const shouldResolveLatest = useRef(!searchParams.get('conversation_id')).current

  const latestQuery = useLatestConversationId(shouldResolveLatest)
  useEffect(() => {
    const id = latestQuery.data
    if (!shouldResolveLatest || conversationId || !id) return
    setSearchParams(
      (prev) => {
        if (prev.get('conversation_id')) return prev
        const p = new URLSearchParams(prev)
        p.set('conversation_id', id)
        return p
      },
      { replace: true },
    )
  }, [latestQuery.data, conversationId, shouldResolveLatest, setSearchParams])

  const graphQuery = useConversationGraph(conversationId)
  const verifQuery = useConversationVerifications(conversationId)
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
  useEffect(() => setSelectedNodeId(''), [conversationId])

  // 选中对话的标题（选择框显示用）
  const conversationQuery = useQuery({
    queryKey: ['exploration', 'conversation', conversationId],
    queryFn: () => getConversation(conversationId),
    enabled: conversationId !== '',
    retry: false,
  })
  const selectConversation = (id: string) => setSearchParams(id ? { conversation_id: id } : {}, { replace: true })

  const flow = useMemo(
    () => (graph ? buildFlowGraph(graph, filterKind, filterState, verificationsByNode) : { nodes: [], edges: [], stats: EMPTY_STATS }),
    [graph, filterKind, filterState, verificationsByNode],
  )
  const view = useMemo(() => applyFocus(flow, selectedNodeId), [flow, selectedNodeId])

  const refresh = () => {
    if (!conversationId) return
    void graphQuery.refetch()
    void verifQuery.refetch()
  }

  const resolvingLatest = shouldResolveLatest && !conversationId && latestQuery.isPending
  const loading = conversationId !== '' && graphQuery.isPending
  const error =
    conversationId !== '' && graphQuery.isError
      ? graphQuery.error instanceof Error
        ? graphQuery.error.message
        : '加载失败'
      : ''
  const hasGraph = (graph?.nodes.length ?? 0) > 0
  const selectedNode = selectedNodeId ? graph?.nodes.find((n) => n.id === selectedNodeId) : undefined

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader title="探索图" subtitle="每个任务一张图：智能体的目标、行动与结论如何达成" />

      <Toolbar
        conversationId={conversationId}
        conversationLabel={conversationQuery.data ? conversationQuery.data.title || conversationQuery.data.id : undefined}
        onConversationChange={selectConversation}
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
              {resolvingLatest ? '定位最新会话' : '加载中'}
            </span>
          </div>
        ) : error ? (
          <div className="flex h-full flex-col items-center justify-center gap-4">
            <div className="flex h-16 w-16 items-center justify-center rounded-full bg-status-error/10">
              <AlertCircle className="h-8 w-8 text-status-error" />
            </div>
            <div className="text-lg font-medium text-status-error">{error}</div>
            <div className="max-w-md text-center text-sm text-text-secondary">
              对话不存在或已删除，请在上方重新选择对话
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
                {conversationId ? '这个对话还没有探索图（任务尚未开始规划，或是纯聊天对话）' : '在上方选择一个对话，查看它的探索图'}
              </div>
            </div>
          </div>
        ) : (
          <GraphCanvas nodes={view.nodes} edges={view.edges} resetKey={conversationId} onNodeClick={setSelectedNodeId} />
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
