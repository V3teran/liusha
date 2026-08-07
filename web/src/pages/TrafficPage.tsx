import { useMemo, useState } from 'react'
import { TrafficFilterBar } from '@/features/traffic/TrafficFilterBar'
import { TrafficTable } from '@/features/traffic/TrafficTable'
import { TrafficDrawer } from '@/features/traffic/TrafficDrawer'
import { useTrafficFilters } from '@/features/traffic/useTrafficFilters'
import { TRAFFIC_PAGE_SIZE, useTrafficDetail, useTrafficHosts, useTrafficList } from '@/features/traffic/useTrafficQueries'
import type { TrafficSummary } from '@/api/types'
import { compactNumber } from '@/lib/format'

// 流量模块：代理捕获流量（proxy_traffic）全局只读浏览。按 host 归属、先于 task，是被分析的输入。
// 布局：标题栏 + 筛选栏（主机/方法/路径/状态范围）+ 单张扁平密集表 + offset 分页。点行右侧抽屉钻取完整原文。
//
// 数据层走 React Query（见 useTrafficQueries）：query key 含 filters/page，任一变化即视为新查询，
// 过期请求自动被丢弃；keepPreviousData 让筛选/翻页时旧数据先留屏，不必每次整表闪骨架。
// 筛选/分页状态持久化进 URL（见 useTrafficFilters）：刷新/前进后退/分享链接都能还原视图。
export function TrafficPage() {
  const { filters, page, setFilters, setPage, resetFilters } = useTrafficFilters()

  const hostsQuery = useTrafficHosts()
  const listQuery = useTrafficList(filters, page)

  const rows = listQuery.data?.items ?? []
  const total = listQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / TRAFFIC_PAGE_SIZE))
  const hasFilter = !!(filters.host || filters.method || filters.path || filters.statusMin || filters.statusMax)

  // host 列只在结果确实跨多个 host 时显示——单 host 时每行重复同值是噪声（同 showProvider 思路）。
  const showHost = useMemo(() => new Set(rows.map((r) => r.host)).size > 1, [rows])

  // 详情钻取：选中行 id 驱动查询，抽屉开合与数据加载解耦。
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const detailQuery = useTrafficDetail(selectedId)
  const openDetail = (row: TrafficSummary) => {
    setSelectedId(row.id)
    setDrawerOpen(true)
  }

  const error = listQuery.isError ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败') : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-4.5 border-b border-border px-5.5 py-3">
        <h1 className="text-sm font-semibold text-text">流量</h1>
        <span className="text-[12.5px] text-muted">代理捕获的真实流量 · 按主机归属</span>
        <span className="ml-auto text-[12.5px] text-muted tabular-nums">共 {compactNumber(total)} 条</span>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-5.5 py-4.5">
        {error ? (
          <div className="py-16 text-center text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : (
          <>
            <TrafficFilterBar
              filters={filters}
              hosts={hostsQuery.data ?? []}
              onChange={setFilters}
              onReset={resetFilters}
              trailing={
                <>
                  第 {page} / {totalPages} 页 · 本页 {rows.length} 条
                </>
              }
            />

            <TrafficTable
              rows={rows}
              loading={listQuery.isPending}
              hasFilter={hasFilter}
              showHost={showHost}
              onRowClick={openDetail}
            />

            {(rows.length > 0 || page > 1) && (
              <div className="flex items-center justify-center gap-3.5 pb-1 pt-3.5">
                <button
                  type="button"
                  disabled={page <= 1 || listQuery.isFetching}
                  onClick={() => setPage(page - 1)}
                  className="rounded-md border border-border px-3 py-1 text-xs text-text disabled:opacity-40"
                >
                  上一页
                </button>
                <span className="text-[12.5px] text-muted tabular-nums">
                  第 {page} / {totalPages} 页
                </span>
                <button
                  type="button"
                  disabled={page >= totalPages || listQuery.isFetching}
                  onClick={() => setPage(page + 1)}
                  className="rounded-md border border-border px-3 py-1 text-xs text-text disabled:opacity-40"
                >
                  下一页
                </button>
              </div>
            )}
          </>
        )}
      </div>

      <TrafficDrawer
        open={drawerOpen}
        detail={detailQuery.data ?? null}
        loading={detailQuery.isPending && selectedId != null}
        error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
        onOpenChange={setDrawerOpen}
      />
    </div>
  )
}
