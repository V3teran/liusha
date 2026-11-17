import { useQuery } from '@tanstack/react-query'
import { getProxyStatus } from '@/api/proxy'

// 轮询周期：代理每 10 秒心跳一次，状态灯随之刷新。
const POLL_MS = 15_000

export const proxyKeys = {
  status: () => ['proxy', 'status'] as const,
}

/** 代理的运行状态：代理流量页与对话页「代理捕获」共用同一份缓存。 */
export function useProxyStatus() {
  return useQuery({ queryKey: proxyKeys.status(), queryFn: getProxyStatus, refetchInterval: POLL_MS })
}
