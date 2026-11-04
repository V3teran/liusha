/**
 * 探索图模块 React Query hooks（对齐 useTaskQueries 约定）。
 *
 * 探索图是运行时动态图，任务推进中节点持续增长：存在 running 节点时
 * 10s 轮询自动跟进，图静止后停轮询。
 */

import { useQuery } from '@tanstack/react-query'
import { getConversationGraph, getConversationVerifications } from '@/api/explorationgraph'
import { listConversations } from '@/api/conversations'

const explorationKeys = {
  graph: (id: string) => ['exploration', 'graph', id] as const,
  verifications: (id: string) => ['exploration', 'verifications', id] as const,
  latestConversation: () => ['exploration', 'latest-conversation'] as const,
}

/** 会话的完整探索图。conversationId 为空（未选定）不发请求。 */
export function useConversationGraph(conversationId: string) {
  return useQuery({
    queryKey: explorationKeys.graph(conversationId),
    queryFn: () => getConversationGraph(conversationId),
    enabled: conversationId !== '',
    refetchInterval: (query) =>
      query.state.data?.nodes.some((n) => n.state === 'running') ? 10_000 : false,
  })
}

/** 验证审计链（node_id → 最新裁决）。best-effort：失败时消费侧按空表处理，不阻塞图。 */
export function useConversationVerifications(conversationId: string) {
  return useQuery({
    queryKey: explorationKeys.verifications(conversationId),
    queryFn: () => getConversationVerifications(conversationId),
    enabled: conversationId !== '',
    retry: false,
  })
}

/**
 * 默认会话解析：URL 未带 conversation_id 时取最新会话（updated_at DESC 首条）。
 * enabled 由调用方以「挂载时是否缺 id」的快照控制——用户清空输入框后不应再自动回填。
 */
export function useLatestConversationId(enabled: boolean) {
  return useQuery({
    queryKey: explorationKeys.latestConversation(),
    queryFn: async () => {
      const { conversations } = await listConversations(1, 0, '')
      return conversations[0]?.id ?? ''
    },
    enabled,
    staleTime: Infinity,
    retry: false,
  })
}
