import { PageHeader } from '@/components/PageHeader'
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
import { Button } from '@/components/ui/Button'

// 任务模块：assignment（批次）→ task（执行实体）的列表视图。人工下发 1 assignment → N task，
// 代理捕获 1 assignment → 1 task；每 task 绑一个对话（详情板跳转）。
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
      <PageHeader title="任务" subtitle="批次与任务的下发、进度与运行控制">
        <Button onClick={() => setDialogOpen(true)}>
          <Plus strokeWidth={2.4} />
          新建任务
        </Button>
      </PageHeader>

      {/* 左右分栏（master-detail）：任务详情是窄格式内容（KV + 短文本 + 按钮），
          右侧常驻面板是业界主流（扫列表、右钻详情保持行上下文，不丢选中态）。
          左列 = 筛选栏（固定）+ 表格滚动区；列宽有设计像素下限 + 容器横向滚动，
          窄屏时出横向滑轮而非挤压变形。 */}
      <div className="flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
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
          {/* 方向键导航挂在滚动容器上：从聚焦行冒泡而来（grid 语义在表格本身）。 */}
          <div
            onKeyDown={onListKeyDown}
            className="min-h-0 flex-1 overflow-y-auto px-6 py-4 outline-none"
          >
            {listError ? (
              <div className="py-16 text-center text-sm text-sev-critical">⚠ {listError}</div>
            ) : (
              <TaskTable
                rows={rows}
                loading={listQuery.isPending}
                hasFilter={hasFilter}
                offset={(page - 1) * size}
                selectedId={selectedId}
                onRowClick={selectRow}
                onOperated={() => void listQuery.refetch()}
              />
            )}
          </div>
        </div>

        {selectedId && (
          <aside className="w-[440px] shrink-0">
            <TaskDetailPane
              task={detail}
              loading={detailQuery.isPending && !rowTask}
              error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
              onClose={closeDetail}
              onOperated={() => {
                void detailQuery.refetch()
                void listQuery.refetch()
              }}
            />
          </aside>
        )}
      </div>

      <NewTaskDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </div>
  )
}
