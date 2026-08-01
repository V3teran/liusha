import { useEffect, useRef } from 'react'
import { useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { getAttackGraph } from '@/api/client'
import type { AttackGraph } from '@/api/types'
import { connectEventStream } from '@/hooks/useEventStream'

// 执行图数据层：react-query 管数据获取/loading/error/竞态（取代手写 state 机器），
// SSE 驱动“何时重新投影”（取代 3 秒定时轮询整图）。
//
// 旧实现的轮询有两个问题：
//   1. 无论有没有新事件，3 秒固定拉一次全量图——扫描空转时纯浪费；扫描密集时反而拉不够快。
//   2. 旧版曾用「节点/边数」签名判断要不要重渲染，但 tool_result 把某节点从 done 翻成
//      failed 时数量不变、签名不变，界面永不更新——状态翻转在实时模式下会丢失。
// 新实现：SSE 收到会话新消息帧 → invalidate 这个 query → react-query 重新 fetch，
// 每次事件都触发真实重拉（不再靠数量签名判断"要不要更新"，直接信新数据本身）。
// 扫描结束（running=false）后既不再监听 SSE，也不再有事件驱动重拉——终态图不会变。

const attackGraphKeys = {
  detail: (owner: string) => ['attack-graph', owner] as const,
}

export function attackGraphQueryKey(owner: string) {
  return attackGraphKeys.detail(owner)
}

/**
 * useAttackGraphQuery 拉取执行图；扫描进行中（服务端返回 running=true）且 live=true 时，
 * 订阅该图绑定会话的 SSE，收到新帧即触发重拉。扫描终态或用户关闭"实时"开关则断开订阅。
 */
export function useAttackGraphQuery(owner: string, live: boolean): UseQueryResult<AttackGraph> {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: attackGraphKeys.detail(owner),
    queryFn: () => getAttackGraph(owner),
    enabled: !!owner,
  })

  const convID = query.data?.conversation_id ?? ''
  const running = query.data?.running ?? false
  const shouldStream = !!owner && !!convID && running && live

  // ref 让 effect 不必把 queryClient/owner 之外的东西塞进依赖数组——SSE 回调只需要
  // "触发一次 invalidate"，不需要闭包捕获当时的 query 状态。
  const ownerRef = useRef(owner)
  ownerRef.current = owner

  useEffect(() => {
    if (!shouldStream) return
    const handle = connectEventStream(convID, {
      // 任意新帧（想/做/得/漏洞……）到达即认为图可能已变化，重新投影。
      // 后端 Project 是纯读投影、开销可控，且这里已经用 running 门控只在扫描进行中订阅，
      // 扫描结束就不再有帧、不再重拉。
      onMessage: () => {
        void queryClient.invalidateQueries({ queryKey: attackGraphKeys.detail(ownerRef.current) })
      },
    })
    return () => handle.close()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shouldStream, convID, queryClient])

  return query
}
