import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { getAttackGraph } from '@/api/client'
import type { AttackGraph } from '@/api/types'

// 攻击图数据层：react-query 管数据获取/loading/error/竞态。
//
// 与旧执行图不同：世界模型无会话轴（图按 assignment 归属，不绑单条会话），故不再 SSE 驱动重投影。
// 图只在 Verifier 坐实新节点时增长——live=true 时轮询兜底（交战进行中图会长），关则只拉一次。
const attackGraphKeys = {
  detail: (taskID: string) => ['attack-graph', taskID] as const,
}

export function attackGraphQueryKey(taskID: string) {
  return attackGraphKeys.detail(taskID)
}

// 交战进行中的轮询间隔（ms）：世界模型只在坐实节点时增长，无需高频。
const POLL_INTERVAL_MS = 5000

/**
 * useAttackGraphQuery 拉取攻击图（世界模型投影）。live=true 时定时轮询（交战进行中图会增长），
 * 关闭则只拉一次静态图。
 */
export function useAttackGraphQuery(taskID: string, live: boolean): UseQueryResult<AttackGraph> {
  return useQuery({
    queryKey: attackGraphKeys.detail(taskID),
    queryFn: () => getAttackGraph(taskID),
    enabled: !!taskID,
    refetchInterval: live && taskID ? POLL_INTERVAL_MS : false,
  })
}
