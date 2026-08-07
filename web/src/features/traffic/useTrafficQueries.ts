import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getTrafficDetail, listTraffic, listTrafficHosts } from '@/api/client'
import type { TrafficFilters } from '@/api/types'

// 流量浏览页数据层：统一走 React Query，取代手写 loading/error/竞态处理。
// query key 含 filters + page：任一变化即视为新查询，过期请求的结果自动被丢弃。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留屏，不必每次整表闪骨架。

export const TRAFFIC_PAGE_SIZE = 50

const trafficKeys = {
  hosts: ['traffic', 'hosts'] as const,
  list: (filters: TrafficFilters, page: number) => ['traffic', 'list', filters, page] as const,
  detail: (id: number | null) => ['traffic', 'detail', id] as const,
}

/** host 下拉候选：全表 distinct，不受当前筛选/分页影响。 */
export function useTrafficHosts() {
  return useQuery({
    queryKey: trafficKeys.hosts,
    queryFn: () => listTrafficHosts(),
  })
}

/** 分页列表：吃筛选 + page 号（offset 分页）。 */
export function useTrafficList(filters: TrafficFilters, page: number) {
  return useQuery({
    queryKey: trafficKeys.list(filters, page),
    queryFn: () => listTraffic(page, TRAFFIC_PAGE_SIZE, filters),
    placeholderData: keepPreviousData,
  })
}

/** 单条完整原文（含 body/headers），点击行按需拉。id=null 时不发请求。 */
export function useTrafficDetail(id: number | null) {
  return useQuery({
    queryKey: trafficKeys.detail(id),
    queryFn: () => getTrafficDetail(id as number),
    enabled: id != null,
  })
}
