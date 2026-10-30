import { useCallback, useEffect, useMemo, useState } from 'react'
import { useListKeyboardNav } from '@/hooks/useListKeyboardNav'
import { useSearchParams } from 'react-router-dom'
import { Plus } from 'lucide-react'
import { TaskFilterBar } from '@/features/tasks/TaskFilterBar'
import { TaskTable } from '@/features/tasks/TaskTable'
import { TaskDetailPane } from '@/features/tasks/TaskDetailPane'
import { NewTaskDialog } from '@/features/tasks/NewTaskDialog'
import { useTaskFilters } from '@/features/tasks/useTaskFilters'
import { useTaskDetail, useTaskList } from '@/features/tasks/useTaskQueries'
import type { TaskSummary } from '@/api/types'
import { PaginationToolbar } from '@/components/ui/PaginationToolbar'

// 任务模块：assignment（批次）→ task（执行实体）的列表视图。主动下发 1 assignment → N task，
// 被动代理 1 assignment → 1 task；每 task 绑一个对话（详情板跳转）。
//
// 布局：左列表（筛选 + 10 列表 + 分页）+ 右详情板（点行选中，Esc/✕ 关闭）。
// 筛选/分页态进 URL（useTaskFilters），列表含 active 任务时 15s 轮询推进状态/进度。
export function TasksPage() {
  const { filters, page, size, setFilters, setPage, setSize, resetFilters } = useTaskFilters()
  const listQuery = useTaskList(filters, page, size)

  const rows = useMemo(() => listQuery.data?.items ?? [], [listQuery.data])
  const total = listQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / size))
  const hasFilter = !!(filters.source || filters.status || filters.search)

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const detailQuery = useTaskDetail(selectedId)
  // ?task=<id>（对话列表 ⋯ 菜单「任务」跳转）：进页即选中该任务打开详情板
  const [searchParams] = useSearchParams()
  const preselectTask = searchParams.get('task')
  useEffect(() => {
    if (preselectTask) setSelectedId(preselectTask)
  }, [preselectTask])
  // 列表行已带全量派生列，详情请求返回前先用行数据撑住详情板（零闪跳）。
  const rowTask = useMemo(() => rows.find((r) => r.id === selectedId) ?? null, [rows, selectedId])
  const detail = detailQuery.data ?? rowTask

  const [dialogOpen, setDialogOpen] = useState(false)

  const selectRow = useCallback((row: TaskSummary) => setSelectedId(row.id), [])
  const closeDetail = useCallback(() => setSelectedId(null), [])

  // 键盘导航（↑/↓ 移动选中、Esc 清除）——两页同构，抽 useListKeyboardNav
  const { onListKeyDown } = useListKeyboardNav(rows, selectedId, setSelectedId)

  const listError = listQuery.isError ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败') : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">任务</h1>
        </div>
        <button
          type="button"
          onClick={() => setDialogOpen(true)}
          className="inline-flex items-center gap-1.5 rounded-lg bg-accent-soft px-3.5 py-2 text-[13px] font-semibold text-accent transition-all hover:bg-accent-softer hover:outline hover:outline-1 hover:outline-accent-soft"
        >
          <Plus className="h-3.5 w-3.5" strokeWidth={2.4} />
          新建任务
        </button>
      </header>

      {/* 左右分栏（master-detail）：任务详情是窄格式内容（KV + 短文本 + 按钮），
          右侧常驻面板是业界主流（Sentry/Datadog/Linear——扫列表、右钻详情保持行上下文）。
          列表列宽有 min-width 兜底 + 容器横向滚动，窄屏时出横向滑轮而非挤压变形
          （「详情放下方」是流量页为承载整条 raw 报文这类宽格式内容的选择，任务详情不需要）。 */}
      <div className="flex min-h-0 flex-1">
        <div
          role="grid"
          tabIndex={0}
          onKeyDown={onListKeyDown}
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto px-6 py-4 outline-none"
        >
          {listError ? (
            <div className="py-16 text-center text-sm text-sev-critical">⚠ {listError}</div>
          ) : (
            <>
              <TaskFilterBar
                filters={filters}
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
              <TaskTable
                rows={rows}
                loading={listQuery.isPending}
                hasFilter={hasFilter}
                offset={(page - 1) * size}
                selectedId={selectedId}
                onRowClick={selectRow}
              />
            </>
          )}
        </div>

        {selectedId && (
          <aside className="w-[440px] shrink-0">
            <TaskDetailPane
              task={detail}
              loading={detailQuery.isPending && !rowTask}
              error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
              onClose={closeDetail}
            />
          </aside>
        )}
      </div>

      <NewTaskDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </div>
  )
}
