import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listFindings } from '@/api/findings'
import type { FindingFilters } from '@/api/types'

// 漏洞台账数据层：统一走 React Query（对齐流量模块 useTrafficQueries 的模式）。
// query key 含 filters + page + size：任一变化即视为新查询，过期请求的结果自动被丢弃。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留屏，不必每次整表闪骨架。

const findingKeys = {
  list: (filters: FindingFilters, page: number, size: number) => ['findings', 'list', filters, page, size] as const,
}

/** 分页列表：吃筛选 + page 号 + 每页条数（offset 分页，seq desc）。 */
export function useFindingList(filters: FindingFilters, page: number, size: number) {
  return useQuery({
    queryKey: findingKeys.list(filters, page, size),
    queryFn: () => listFindings({ ...filters, page, size }),
    placeholderData: keepPreviousData,
  })
}
