import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { createTasks } from '@/api/client'
import type { TaskCreateResponse } from '@/api/types'
import { cn } from '@/lib/utils'
import { TASK_LIST_KEY } from './useTaskQueries'

interface NewTaskDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const INPUT_CLASS =
  'h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-text outline-none transition-all placeholder:text-text-tertiary focus:border-accent focus:glow-accent'

/** cron 五段式人话预览（常见形态；其余原样展示）。 */
function cronHuman(v: string): string {
  const p = v.trim().split(/\s+/)
  if (p.length !== 5) return '无效表达式'
  const [mi, h, dom, mon, dow] = p
  if (!/^\d+$/.test(mi) || !/^\d+$/.test(h)) return v
  const hm = `${h.padStart(2, '0')}:${mi.padStart(2, '0')}`
  const D = ['日', '一', '二', '三', '四', '五', '六']
  if (dom === '*' && mon === '*' && dow === '*') return `每天 ${hm}`
  if (dom === '*' && mon === '*' && /^\d+$/.test(dow)) return `每周${D[+dow % 7]} ${hm}`
  if (dow === '*' && mon === '*' && /^\d+$/.test(dom)) return `每月 ${dom} 日 ${hm}`
  return v
}

/**
 * LinedTextarea —— 带行号槽的多行输入（「每行一个任务」的可视化）。
 * 左侧行号随物理行数增减，滚动与 textarea 同步。
 */
function LinedTextarea({
  value,
  onChange,
  onKeyDown,
}: {
  value: string
  onChange: (v: string) => void
  onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
}) {
  const gutterRef = useRef<HTMLDivElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const lineCount = useMemo(() => value.split('\n').length, [value])

  return (
    <div className="flex overflow-hidden rounded-lg border border-border bg-surface transition-all focus-within:border-accent">
      <div
        ref={gutterRef}
        aria-hidden
        className="w-9 shrink-0 select-none overflow-hidden border-r border-border-subtle bg-surface-2/60 py-2.5 text-right font-mono text-[11px] leading-[1.6] text-text-tertiary"
      >
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i} className="pr-2.5">
            {i + 1}
          </div>
        ))}
      </div>
      <textarea
        ref={taRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        onScroll={() => {
          if (gutterRef.current && taRef.current) gutterRef.current.scrollTop = taRef.current.scrollTop
        }}
        rows={5}
        placeholder={'每行一个任务，例如：\n对 http://目标一 做渗透测试，重点检查注入\n对 http://目标二 做指纹识别与端口排查'}
        className="min-h-[110px] flex-1 resize-y border-0 bg-transparent px-3 py-2.5 text-[13px] leading-[1.6] text-text outline-none placeholder:text-text-tertiary"
        aria-label="任务目标，每行一个"
      />
    </div>
  )
}

/** 新建任务弹层：批次名称/描述 + 每行一个目标的清单 + 可选 cron 定时。 */
export function NewTaskDialog({ open, onOpenChange }: NewTaskDialogProps) {
  const qc = useQueryClient()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [briefsText, setBriefsText] = useState('')
  const [cronOn, setCronOn] = useState(false)
  const [cron, setCron] = useState('0 9 * * 1')
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
      createTasks({ title: title.trim(), description: description.trim(), briefs, cron: cronOn ? cron.trim() : '' }),
    onSuccess: (resp: TaskCreateResponse) => {
      qc.invalidateQueries({ queryKey: TASK_LIST_KEY })
      qc.invalidateQueries({ queryKey: ['conversations'] })
      setSuccess(
        resp.scheduled
          ? `已创建定时批次，首次触发：${resp.next_run_at ? new Date(resp.next_run_at).toLocaleString() : '—'}`
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

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 w-[620px] max-w-[calc(100vw-48px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-surface shadow-lg outline-none">
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-sm font-semibold text-text">新建任务</Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <div className="flex flex-col gap-4 px-5 py-4">
            <div>
              <label htmlFor="nt-title" className="mb-1.5 block text-[12.5px] font-semibold text-text">
                名称
              </label>
              <input id="nt-title" className={INPUT_CLASS} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="如：DVWA 专项测试" />
            </div>

            <div>
              <label htmlFor="nt-desc" className="mb-1.5 block text-[12.5px] font-semibold text-text">
                描述
              </label>
              <input id="nt-desc" className={INPUT_CLASS} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="可选" />
            </div>

            <div>
              <div className="mb-1.5 flex items-baseline justify-between">
                <label htmlFor="nt-briefs" className="text-[12.5px] font-semibold text-text">
                  任务目标
                </label>
                <span className="text-[11.5px] text-text-tertiary">每行一个任务</span>
              </div>
              <LinedTextarea
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
              <label className="inline-flex cursor-pointer items-center gap-2 text-[12.5px] font-semibold text-text">
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
                    className={cn(INPUT_CLASS, 'w-32 font-mono text-[12.5px]')}
                    placeholder="0 9 * * 1"
                    aria-label="cron 表达式"
                  />
                  <span className="text-xs text-text-secondary">{cronHuman(cron)}</span>
                </span>
              )}
            </div>

            {success && <div className="text-xs text-status-success">✓ {success}</div>}
            {error && <div className="text-xs text-sev-critical">⚠ {error}</div>}
          </div>

          <div className="flex items-center justify-end gap-2.5 border-t border-border bg-surface-2/50 px-5 py-3.5">
            <span className="mr-auto text-[11px] text-text-tertiary">
              {briefs.length > 0 ? `共 ${briefs.length} 个任务` : '至少写一行任务目标'}
              <span className="ml-2 opacity-75">⌘/Ctrl+Enter 创建</span>
            </span>
            <Dialog.Close asChild>
              <button type="button" className="rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-text-secondary transition-colors hover:text-text">
                取消
              </button>
            </Dialog.Close>
            <button
              type="button"
              onClick={submit}
              disabled={briefs.length === 0 || mutation.isPending}
              className={cn(
                'rounded-lg bg-accent-soft px-3.5 py-1.5 text-[13px] font-semibold text-accent transition-all',
                'hover:bg-accent-softer hover:outline hover:outline-1 hover:outline-accent-soft',
                (briefs.length === 0 || mutation.isPending) && 'cursor-default opacity-45 hover:bg-accent-soft hover:outline-none',
              )}
            >
              {mutation.isPending ? '创建中…' : '创建'}
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
