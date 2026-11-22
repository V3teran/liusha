import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listAuditEvents, type AuditFilters } from '@/api/audit'

// 审计页数据层：统一走 React Query。query key 含搜索 + 筛选 + 分页，任一变化即视为新查询
// （过期请求的结果自动被丢弃）；placeholderData 让翻页时旧数据先留在屏幕上不闪骨架。

const auditKeys = {
  list: (q: string, filters: AuditFilters, page: number, size: number) =>
    ['audit', 'list', q, filters, page, size] as const,
}

export function useAuditList(params: { query: string; page: number; size: number } & AuditFilters) {
  const { query, page, size, ...filters } = params
  return useQuery({
    queryKey: auditKeys.list(query, filters, page, size),
    queryFn: () => listAuditEvents(query, page, size, filters),
    placeholderData: keepPreviousData,
  })
}
