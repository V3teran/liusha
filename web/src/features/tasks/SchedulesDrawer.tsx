import { useEffect, useRef, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { CalendarClock, Trash2, X } from 'lucide-react'
import { errorText } from '@/api/client'
import type { ScheduleRow } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Switch } from '@/components/ui/Switch'
import { fullTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { cronHuman } from './cron'
import { useDeleteSchedule, useSchedules, useToggleSchedule } from './useScheduleQueries'

interface SchedulesDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 打开时要定位的模板（任务详情「定时」标记跳转而来）；不在列表中说明模板已删除。 */
  focusId: string | null
}

/** 清单里直接展开的目标条数上限；其余折叠成「另 N 个」，避免长批次把列表撑成一屏一个。 */
const BRIEF_PREVIEW = 3

// 定时模板管理：「新建任务」开启定时执行后创建的模板在这里查看、启停与删除。
// 模板到点按清单每条展开一个任务；停用即不再触发，重新启用后下次触发从当时起按 cron 重算。
export function SchedulesDrawer({ open, onOpenChange, focusId }: SchedulesDrawerProps) {
  const query = useSchedules(open)
  const toggle = useToggleSchedule()
  const remove = useDeleteSchedule()
  const [pendingDelete, setPendingDelete] = useState<ScheduleRow | null>(null)
  const [actionError, setActionError] = useState('')

  useEffect(() => {
    if (open) setActionError('')
  }, [open])

  const rows = query.data ?? []
  const focusMissing = !!focusId && query.isSuccess && !rows.some((r) => r.id === focusId)

  const onToggle = (r: ScheduleRow, enabled: boolean) => {
    setActionError('')
    toggle.mutate({ id: r.id, enabled }, { onError: (e) => setActionError(errorText(e, '切换失败')) })
  }
  const onDelete = (r: ScheduleRow) => {
    setActionError('')
    remove.mutate(r.id, { onError: (e) => setActionError(errorText(e, '删除失败')) })
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed inset-y-0 right-0 flex h-full w-full flex-col bg-surface shadow-2xl focus:outline-none sm:max-w-[560px]">
          <header className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
            <div className="min-w-0">
              <Dialog.Title className="flex items-center gap-2 text-base font-semibold text-text">
                <CalendarClock className="h-4 w-4 text-accent" aria-hidden />
                定时模板
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-xs leading-relaxed text-text-secondary">
                到点按清单每条展开一个任务。停用后不再触发；重新启用从当时起按 cron 计算下次触发，停用期间错过的不补跑。
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </header>

          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
            {actionError && (
              <p role="alert" className="mb-3 rounded-lg border border-sev-critical/30 bg-sev-critical/5 px-3 py-2 text-xs text-sev-critical">
                {actionError}
              </p>
            )}
            {focusMissing && (
              <p className="mb-3 rounded-lg border border-border bg-surface-2 px-3 py-2 text-xs text-text-secondary">
                该任务的来源模板已删除，任务本身不受影响。
              </p>
            )}
            {query.isPending ? (
              <p className="py-16 text-center text-sm text-text-tertiary">加载中…</p>
            ) : query.isError ? (
              <p role="alert" className="py-16 text-center text-sm text-sev-critical">
                ⚠ {errorText(query.error, '加载定时模板失败')}
              </p>
            ) : rows.length === 0 ? (
              <p className="py-16 text-center text-sm leading-relaxed text-text-tertiary">
                还没有定时模板。
                <br />
                新建任务时开启「定时执行」即可创建。
              </p>
            ) : (
              <ul className="flex flex-col gap-3">
                {rows.map((r) => (
                  <li key={r.id}>
                    <ScheduleCard
                      row={r}
                      focused={r.id === focusId}
                      busy={(toggle.isPending && toggle.variables?.id === r.id) || (remove.isPending && remove.variables === r.id)}
                      onToggle={(v) => onToggle(r, v)}
                      onDelete={() => setPendingDelete(r)}
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>

          <ConfirmDialog
            open={pendingDelete != null}
            onOpenChange={(v) => !v && setPendingDelete(null)}
            title="删除定时模板"
            message={
              <>
                删除「{pendingDelete ? scheduleName(pendingDelete) : ''}」后不再触发。已触发的任务保留，但不再标为定时触发。
              </>
            }
            confirmLabel="确认删除"
            danger
            onConfirm={() => pendingDelete && onDelete(pendingDelete)}
          />
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

/** 模板展示名：未填标题时取首个目标。 */
function scheduleName(r: ScheduleRow): string {
  return r.title || r.briefs[0] || '未命名模板'
}

interface ScheduleCardProps {
  row: ScheduleRow
  focused: boolean
  busy: boolean
  onToggle: (enabled: boolean) => void
  onDelete: () => void
}

function ScheduleCard({ row, focused, busy, onToggle, onDelete }: ScheduleCardProps) {
  const ref = useRef<HTMLElement>(null)
  const name = scheduleName(row)
  const human = cronHuman(row.cron)
  const hidden = row.briefs.length - BRIEF_PREVIEW

  useEffect(() => {
    if (focused) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [focused])

  return (
    <article
      ref={ref}
      aria-current={focused ? 'true' : undefined}
      className={cn(
        'rounded-xl border bg-surface p-4 transition-colors',
        focused ? 'border-accent shadow-[var(--glow-accent)]' : 'border-border',
        !row.enabled && 'bg-surface-2',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className={cn('truncate text-sm font-semibold', row.enabled ? 'text-text' : 'text-text-secondary')}>{name}</h3>
          {row.description && <p className="mt-0.5 line-clamp-2 text-xs text-text-tertiary">{row.description}</p>}
        </div>
        <Switch
          checked={row.enabled}
          disabled={busy}
          onCheckedChange={onToggle}
          aria-label={`启用「${name}」`}
          className="mt-0.5"
        />
      </div>

      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
        <dt className="text-text-tertiary">周期</dt>
        <dd className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          {human !== row.cron && <span className="text-text">{human}</span>}
          <code className="font-mono text-text-secondary">{row.cron}</code>
        </dd>
        <dt className="text-text-tertiary">下次</dt>
        <dd className={row.enabled ? 'text-text' : 'text-text-tertiary'}>
          {row.enabled ? (row.next_run_at ? fullTime(row.next_run_at) : '—') : '已停用，不会触发'}
        </dd>
        <dt className="text-text-tertiary">上次</dt>
        <dd className="text-text-secondary">{row.last_run_at ? fullTime(row.last_run_at) : '尚未触发'}</dd>
      </dl>

      <div className="mt-3 border-t border-border pt-3">
        <p className="mb-1.5 text-xs text-text-tertiary">每次触发 {row.briefs.length} 个任务</p>
        <ul className="flex flex-col gap-1">
          {row.briefs.slice(0, BRIEF_PREVIEW).map((b, i) => (
            <li key={i} className="truncate font-mono text-xs text-text-secondary" title={b}>
              {b}
            </li>
          ))}
          {hidden > 0 && <li className="text-xs text-text-tertiary">另 {hidden} 个</li>}
        </ul>
      </div>

      <div className="mt-3 flex justify-end">
        <Button variant="ghost" size="xs" disabled={busy} onClick={onDelete} aria-label={`删除「${name}」`}>
          <Trash2 aria-hidden />
          删除
        </Button>
      </div>
    </article>
  )
}
