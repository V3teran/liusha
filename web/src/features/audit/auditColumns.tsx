import { createColumnHelper } from '@tanstack/react-table'
import type { AuditEvent } from '@/api/audit'
import { cn } from '@/lib/utils'
import { clockTime, dateOnly, fullTime } from '@/lib/format'

const columnHelper = createColumnHelper<AuditEvent>()

/** 表格 meta：# 列页内序号 = startIndex + 行号 + 1。 */
export interface AuditTableMeta {
  readonly startIndex: number
}

// 动作中文文案单点定义（与后端 audit 包词表一一对应；未登记的代码原样展示不丢信息）。
const ACTION_LABEL: Record<string, string> = {
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.logout': '退出',
  'auth.password_change': '修改密码',
  'task.create': '创建任务',
  'task.abort': '中止任务',
  'credential.set': '写入凭证',
  'credential.delete': '删除凭证',
}

/** 目标类型中文文案。 */
const TARGET_LABEL: Record<string, string> = {
  auth: '账号',
  task: '任务',
  credential: '凭证',
}

export function actionLabel(action: string): string {
  return ACTION_LABEL[action] ?? action
}

export function targetLabel(kind: string): string {
  return TARGET_LABEL[kind] ?? kind
}

/**
 * 列定义单点声明列宽。列顺序（审计流水读法：时间→谁→做了什么→对谁→附注）：
 * # → 时间 → 操作者 → 动作 → 目标 → 详情。
 * metadata 就地展示（量小、无需抽屉钻取）；无附注时显示 —。
 */
export function buildAuditColumns() {
  return [
    columnHelper.display({
      id: 'seq',
      header: '#',
      size: 48,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="block text-xs tabular-nums text-text-tertiary">
          {(ctx.table.options.meta as AuditTableMeta).startIndex + ctx.row.index + 1}
        </span>
      ),
    }),
    columnHelper.accessor('created_at', {
      header: '时间',
      size: 104,
      cell: (ctx) => {
        const iso = ctx.getValue()
        return (
          <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-xs font-normal not-italic text-text-tertiary">{clockTime(iso)}</em>
          </span>
        )
      },
    }),
    columnHelper.accessor('actor', {
      header: '操作者',
      size: 140,
      cell: (ctx) => <span className="font-mono text-xs text-text">{ctx.getValue()}</span>,
    }),
    columnHelper.accessor('action', {
      header: '动作',
      size: 110,
      cell: (ctx) => (
        <span
          className={cn(
            'text-xs font-semibold',
            ctx.getValue() === 'auth.login_failed' ? 'text-status-warning' : 'text-text-secondary',
          )}
        >
          {actionLabel(ctx.getValue())}
        </span>
      ),
    }),
    columnHelper.accessor('target_kind', {
      header: '目标',
      size: 220,
      cell: (ctx) => {
        const e = ctx.row.original
        return (
          <span className="flex min-w-0 flex-col">
            <span className="text-xs text-text-secondary">{targetLabel(e.target_kind)}</span>
            <span className="truncate font-mono text-xs text-text-tertiary" title={e.target_id}>
              {e.target_id}
            </span>
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'metadata',
      header: '详情',
      size: 260,
      cell: (ctx) => {
        const meta = ctx.row.original.metadata
        if (!meta || Object.keys(meta).length === 0) {
          return <span className="text-xs text-text-tertiary">—</span>
        }
        const text = JSON.stringify(meta)
        return (
          <span className="block truncate font-mono text-xs text-text-tertiary" title={text}>
            {text}
          </span>
        )
      },
    }),
  ]
}
