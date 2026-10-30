import { useCallback, useMemo, useState } from 'react'
import { useListKeyboardNav } from '@/hooks/useListKeyboardNav'
import { TrafficFilterBar } from '@/features/traffic/TrafficFilterBar'
import { TrafficTable } from '@/features/traffic/TrafficTable'
import { TrafficDetailPane } from '@/features/traffic/TrafficDetailPane'
import { useTrafficFilters } from '@/features/traffic/useTrafficFilters'
import { useTrafficContentTypes, useTrafficDetail, useTrafficList } from '@/features/traffic/useTrafficQueries'
import type { TrafficSummary } from '@/api/types'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'

// 流量模块：代理捕获流量（proxy_traffic）全局只读浏览。按 host 归属、先于 task，是被分析的输入。
// 布局：Burp 式常驻左右分栏——左列表（筛选 + 密集表 + 分页）+ 右详情面板（整条请求/响应报文）。
// 选中行 id 驱动右栏查询；↑↓ 键在当前页内移动选择。取代旧的 Radix Dialog 抽屉（一次只能看一条、遮挡列表）。
//
// 数据层走 React Query（见 useTrafficQueries）：query key 含 filters/page，任一变化即视为新查询，
// 过期请求自动被丢弃；keepPreviousData 让筛选/翻页时旧数据先留屏。筛选/分页态持久化进 URL。
export function TrafficPage() {
  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useTrafficFilters()

  const contentTypesQuery = useTrafficContentTypes()
  const listQuery = useTrafficList(filters, page, size)

  const rows = useMemo(() => listQuery.data?.items ?? [], [listQuery.data])
  const total = listQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / size))
  const hasFilter = !!(
    filters.method ||
    filters.contentType ||
    filters.statusClass ||
    filters.search ||
    filters.since ||
    filters.until
  )

  // host 列只在结果确实跨多个 host 时显示——单 host 时每行重复同值是噪声。
  const showHost = useMemo(() => new Set(rows.map((r) => r.host)).size > 1, [rows])

  // 详情钻取：选中行 id 驱动右栏查询。翻页/换筛选后当前选中若不在本页，右栏保留上次结果直到另选。
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const detailQuery = useTrafficDetail(selectedId)
  const selectRow = useCallback((row: TrafficSummary) => setSelectedId(row.id), [])
  const closeDetail = useCallback(() => setSelectedId(null), [])
  const hasSelection = selectedId != null

  // ↑↓ 在当前页内移动选择（列表获焦时）。首次按下且无选中则选中首行。
  // 键盘导航（↑/↓ 移动选中、Esc 清除）——两页同构，抽 useListKeyboardNav
  const { onListKeyDown } = useListKeyboardNav(rows, selectedId, setSelectedId)

  const listError = listQuery.isError ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败') : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">代理流量</h1>
          <p className="mt-0.5 text-sm text-text-secondary">代理捕获的真实流量</p>
        </div>
      </header>

      {/* 上下分栏（对齐 Burp HTTP history）：默认列表占满全高、可浏览全部流量；
          点击某行后才在下方拆出详情面板（详情内部请求/响应再左右并排），关闭后恢复全高。
          未选中时列表 flex-1 独占；选中后列表退为 46% 比例、详情占余下空间。 */}
      <div className="flex min-h-0 flex-1 flex-col">
        <div
          className={`flex min-h-0 flex-col overflow-y-auto px-6 py-4 ${
            hasSelection ? 'shrink-0 basis-[46%] border-b border-border' : 'flex-1'
          }`}
        >
          {listError ? (
            <div className="py-16 text-center text-sm text-sev-critical">⚠ {listError}</div>
          ) : (
            <div role="grid" tabIndex={0} onKeyDown={onListKeyDown} className="flex flex-col outline-none">
              <TrafficFilterBar
                filters={filters}
                contentTypes={contentTypesQuery.data ?? []}
                onChange={setFilters}
                onReset={resetFilters}
                trailing={
                  <PaginationToolbar
                    page={page}
                    pageSize={size}
                    total={total}
                    totalPages={totalPages}
                    onPageChange={setPage}
                    onPageSizeChange={setSize}
                    loading={listQuery.isFetching}
                  />
                }
              />

              <TrafficTable
                rows={rows}
                loading={listQuery.isPending}
                hasFilter={hasFilter}
                showHost={showHost}
                selectedId={selectedId}
                onRowClick={selectRow}
              />
            </div>
          )}
        </div>

        {hasSelection && (
          <div className="flex min-h-0 flex-1 flex-col bg-surface">
            <TrafficDetailPane
              detail={detailQuery.data ?? null}
              loading={detailQuery.isPending}
              error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
              onClose={closeDetail}
            />
          </div>
        )}
      </div>
    </div>
  )
}
