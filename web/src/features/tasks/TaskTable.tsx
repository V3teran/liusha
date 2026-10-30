import type { TaskSummary } from '@/api/types'
import { buildTaskColumns } from './taskColumns'
import { DataTable } from '@/components/DataTable'

interface TaskTableProps {
  rows: TaskSummary[]
  loading: boolean
  hasFilter: boolean
  offset: number // # 序号起点（(page-1)*size）
  selectedId: string | null
  onRowClick: (row: TaskSummary) => void
}

// 任务表：列定义在 taskColumns 单点声明，表外壳（列宽/骨架/空态/行交互）走
// DataTable 通用组件——四张 react-table 页面共用同一骨架。
export function TaskTable({ rows, loading, hasFilter, offset, selectedId, onRowClick }: TaskTableProps) {
  return (
    <DataTable
      rows={rows}
      columns={buildTaskColumns(offset) as never}
      minWidth={1075}
      cellPadding="px-3"
      loading={loading}
      hasFilter={hasFilter}
      emptyText="任务——右上角「新建任务」下发第一批"
      selectedId={selectedId}
      getRowId={(row) => row.id}
      onRowClick={onRowClick}
      rowAriaLabel={(row) => `查看任务详情 ${row.brief.slice(0, 24)}`}
    />
  )
}
