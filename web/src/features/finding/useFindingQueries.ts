import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listFindingHosts, listFindingScenarios, listFindings } from '@/api/client'
import type { FindingFilters } from '@/api/types'

// 漏洞台账数据层：统一走 React Query（对齐流量模块 useTrafficQueries 的模式）。
// query key 含 filters + page + size：任一变化即视为新查询，过期请求的结果自动被丢弃。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留屏，不必每次整表闪骨架。

const findingKeys = {
  hosts: ['findings', 'hosts'] as const,
  scenarios: ['findings', 'scenarios'] as const,
  list: (filters: FindingFilters, page: number, size: number) => ['findings', 'list', filters, page, size] as const,
}

/** host 下拉候选：全表 distinct，不受当前筛选/分页影响。 */
export function useFindingHosts() {
  return useQuery({
    queryKey: findingKeys.hosts,
    queryFn: () => listFindingHosts(),
  })
}

/** 场景下拉候选：全表 distinct scenario_id。 */
export function useFindingScenarios() {
  return useQuery({
    queryKey: findingKeys.scenarios,
    queryFn: () => listFindingScenarios(),
  })
}

/** 分页列表：吃筛选 + page 号 + 每页条数（offset 分页，seq desc）。 */
export function useFindingList(filters: FindingFilters, page: number, size: number) {
  return useQuery({
    queryKey: findingKeys.list(filters, page, size),
    queryFn: () => listFindings({ ...filters, page, size }),
    placeholderData: keepPreviousData,
  })
}
