import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import * as Dialog from '@radix-ui/react-dialog'
import { createTasks } from '@/api/tasks'
import type { TaskCreateResponse } from '@/api/types'
import { cn } from '@/lib/utils'
import { TASK_LIST_KEY } from './useTaskQueries'
import { SCHEDULES_KEY } from './useScheduleQueries'
import { cronHuman } from './cron'
import { Button } from '@/components/ui/Button'
import { FIELD_LABEL_CLASS, INPUT_CLASS } from '@/components/ui/control'
import { ScenarioSelect } from '@/features/scenarios/ScenarioSelect'
import { DialogClose } from '@/components/ui/DialogClose'
import { InlineError } from '@/components/ui/InlineError'
import { useReturnFocus } from '@/hooks/useReturnFocus'

interface NewTaskDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * LinedTextarea —— 带行号槽的多行输入（「每行一个任务」的可视化）。
 * 左侧行号随行数增减，滚动与 textarea 同步。行号与文本共用同一个绝对行高（leading-6），
 * 且文本不自动折行（长行横向滚动）：折行会让一个任务占多个视觉行，编号随之错位。
 */
function LinedTextarea({
  id,
  value,
  onChange,
  onKeyDown,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
}) {
  const gutterRef = useRef<HTMLDivElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const lineCount = useMemo(() => value.split('\n').length, [value])

  return (
    <div className="flex overflow-hidden rounded-lg border border-border bg-surface transition-colors focus-within:border-accent">
      <div
        ref={gutterRef}
        aria-hidden
        className="w-9 shrink-0 select-none overflow-hidden border-r border-border-subtle bg-surface-2/60 py-2.5 text-right font-mono text-2xs leading-6 text-text-tertiary"
      >
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i} className="pr-2.5">
            {i + 1}
          </div>
        ))}
      </div>
      <textarea
        id={id}
        ref={taRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        onScroll={() => {
          if (gutterRef.current && taRef.current) gutterRef.current.scrollTop = taRef.current.scrollTop
        }}
        rows={5}
        wrap="off"
        placeholder={'每行一个任务，例如：\n对 http://目标一 做渗透测试，重点检查注入\n对 http://目标二 做指纹识别与端口排查'}
        className="min-h-[110px] min-w-0 flex-1 resize-y border-0 bg-transparent px-3 py-2.5 text-sm max-sm:text-base leading-6 text-text outline-none placeholder:text-text-tertiary"
      />
    </div>
  )
}

/** 新建任务弹层：批次名称 + 每行一个目标的清单 + 可选 cron 定时（定时才有模板描述）。 */
export function NewTaskDialog({ open, onOpenChange }: NewTaskDialogProps) {
  const qc = useQueryClient()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [briefsText, setBriefsText] = useState('')
  const [cronOn, setCronOn] = useState(false)
  const [cron, setCron] = useState('0 9 * * 1')
  const [scenario, setScenario] = useState<string | null>(null) // null = 未选，服务端落定缺省场景
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('') // 成功态：短暂展示后自动关弹层（无全局 toast 基建，就近反馈）

  // 每次打开重置表单（上次草稿不跨会话）。
  useEffect(() => {
    if (open) {
      setTitle('')
      setDescription('')
      setBriefsText('')
      setCronOn(false)
      setCron('0 9 * * 1')
      setScenario(null)
      setError('')
      setSuccess('')
    }
  }, [open])

  const briefs = useMemo(
    () => briefsText.split('\n').map((s) => s.trim()).filter(Boolean),
    [briefsText],
  )

  const mutation = useMutation({
    mutationFn: () =>
      createTasks({
        title: title.trim(),
        briefs,
        ...(cronOn ? { cron: cron.trim(), description: description.trim() } : { cron: '' }),
        scenario: scenario ?? undefined,
      }),
    onSuccess: (resp: TaskCreateResponse) => {
      qc.invalidateQueries({ queryKey: TASK_LIST_KEY })
      qc.invalidateQueries({ queryKey: ['conversations'] })
      if (resp.scheduled) qc.invalidateQueries({ queryKey: SCHEDULES_KEY })
      setSuccess(
        resp.scheduled
          ? `已创建定时模板，首次触发：${resp.next_run_at ? new Date(resp.next_run_at).toLocaleString() : '—'}`
          : `已创建批次，${resp.task_count} 个任务各自绑定独立对话`,
      )
      setTimeout(() => onOpenChange(false), 1200)
    },
    onError: (e: Error) => setError(e.message || '创建失败'),
  })

  const submit = () => {
    if (briefs.length === 0 || mutation.isPending) return
    setError('')
    mutation.mutate()
  }

  const returnFocus = useReturnFocus(open)
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed inset-0 m-auto h-fit w-[620px] max-w-[calc(100vw-48px)] rounded-xl border border-border bg-surface shadow-lg outline-none" onCloseAutoFocus={returnFocus}>
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-base font-semibold text-text">新建任务</Dialog.Title>
            <DialogClose />
          </div>

          <div className="flex flex-col gap-4 px-5 py-4">
            <div>
              <label htmlFor="nt-title" className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
                名称
              </label>
              <input id="nt-title" className={INPUT_CLASS} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="如：DVWA 专项测试" />
            </div>

            <div>
              <label htmlFor="nt-scenario" className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
                场景
              </label>
              <ScenarioSelect id="nt-scenario" value={scenario} onChange={setScenario} className="w-full" />
            </div>

            <div>
              <label htmlFor="nt-briefs" className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
                任务目标
              </label>
              <LinedTextarea
                id="nt-briefs"
                value={briefsText}
                onChange={setBriefsText}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault()
                    submit()
                  }
                }}
              />
            </div>

            <div className="flex flex-wrap items-center gap-3.5">
              <label className={cn(FIELD_LABEL_CLASS, 'inline-flex cursor-pointer items-center gap-2')}>
                <input
                  type="checkbox"
                  checked={cronOn}
                  onChange={(e) => setCronOn(e.target.checked)}
                  className="h-3.5 w-3.5 cursor-pointer accent-[var(--accent)]"
                />
                定时执行
              </label>
              {cronOn && (
                <span className="flex min-w-0 flex-1 items-center gap-2.5">
                  <input
                    value={cron}
                    onChange={(e) => setCron(e.target.value)}
                    className={cn(INPUT_CLASS, 'w-32 font-mono text-xs')}
                    placeholder="0 9 * * 1"
                    aria-label="cron 表达式"
                  />
                  <span className="text-xs text-text-secondary">{cronHuman(cron)}</span>
                </span>
              )}
            </div>

            {cronOn && (
              <div>
                <label htmlFor="nt-desc" className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
                  模板描述
                </label>
                <input id="nt-desc" className={INPUT_CLASS} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="可选" />
              </div>
            )}

            {success && <div className="text-xs text-status-success">✓ {success}</div>}
            {error && <InlineError>{error}</InlineError>}
          </div>

          <div className="flex items-center justify-end gap-2.5 border-t border-border bg-surface-2/50 px-5 py-3.5">
            <span className="mr-auto text-2xs text-text-tertiary">
              {briefs.length > 0 && `共 ${briefs.length} 个任务`}
            </span>
            <Dialog.Close asChild>
              <Button variant="secondary">取消</Button>
            </Dialog.Close>
            <Button onClick={submit} aria-keyshortcuts="Meta+Enter Control+Enter" disabled={briefs.length === 0 || mutation.isPending}>
              {mutation.isPending ? '创建中…' : '创建'}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
