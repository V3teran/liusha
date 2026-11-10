import { useState } from 'react'
import type { TaskSummary } from '@/api/types'
import { controlTask } from '@/api/tasks'
import { buildTaskColumns, briefHead } from './taskColumns'
import { DataTable } from '@/components/DataTable'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'

type ControlAction = 'pause' | 'resume' | 'terminate'

interface TaskTableProps {
  rows: TaskSummary[]
  loading: boolean
  hasFilter: boolean
  offset: number // # 序号起点（(page-1)*size）
  selectedId: string | null
  onRowClick: (row: TaskSummary) => void
  /** 操作列动作完成后的刷新回调（失效任务列表查询）。 */
  onOperated?: () => void
}

// 任务表：列定义在 taskColumns 单点声明，表外壳（列宽/骨架/空态/行交互）走
// DataTable 通用组件——四张 react-table 页面共用同一骨架。
// 确认弹层由本组件持有（列定义无状态，动作上交宿主执行）：只有不可逆的
// 停止过弹层（danger 形态）；暂停/继续可逆，切换钮直接执行。
export function TaskTable({ rows, loading, hasFilter, offset, selectedId, onRowClick, onOperated }: TaskTableProps) {
  const [confirmTarget, setConfirmTarget] = useState<TaskSummary | null>(null)
  const execute = (task: TaskSummary, action: ControlAction) => {
    controlTask(task.id, action)
      .then(() => onOperated?.())
      .catch((e: Error) => window.alert(`操作失败：${e.message}`))
  }
  const handleControl = (task: TaskSummary, action: ControlAction) => {
    if (action === 'terminate') {
      setConfirmTarget(task)
      return
    }
    execute(task, action)
  }
  return (
    <>
      <DataTable
        ariaLabel="任务列表"
        rows={rows}
        columns={buildTaskColumns(offset, handleControl) as never}
        cellPadding="px-3"
        loading={loading}
        hasFilter={hasFilter}
        emptyText="任务——右上角「新建任务」下发第一批"
        selectedId={selectedId}
        getRowId={(row) => row.id}
        onRowClick={onRowClick}
        rowAriaLabel={(row) => `查看任务详情 ${row.brief.slice(0, 24)}`}
      />
      <ConfirmDialog
        open={confirmTarget !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmTarget(null)
        }}
        title="停止任务"
        danger
        confirmLabel="确认停止"
        message={
          confirmTarget
            ? `任务「${briefHead(confirmTarget.brief)}」将落终态，进行中的 agent 立即退出（可重新下发）。`
            : ''
        }
        onConfirm={() => {
          const target = confirmTarget
          setConfirmTarget(null)
          if (target) execute(target, 'terminate')
        }}
      />
    </>
  )
}
