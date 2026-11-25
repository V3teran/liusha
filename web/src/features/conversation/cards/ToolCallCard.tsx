import { ChevronRight, Loader2, CheckCircle2, XCircle, MinusCircle } from 'lucide-react'
import { cn } from '@/lib/utils'

/** 配对的执行结果（一动作一卡：调用与观测同一张卡呈现）。 */
export interface ToolResultInfo {
  text: string
  durationMs: number
  err: string
}

interface ToolCallCardProps {
  tool: string
  args: string
  /** 左侧色条的颜色：取所属智能体的身份色。它所在的卡已经表明是谁调的，卡内不再重复身份标签 */
  accent?: string
  running?: boolean // 执行中：已发 tool_call 未见配对 tool_result（任务活跃时）
  ended?: boolean // 任务已终态：无结果的悬挂 call 是被中断，不是执行中
  result?: ToolResultInfo // 已配对的结果；与 running 互斥
  /** 展开状态由时间线持有：这一轮从在途变成已出终稿时卡片会换位置重挂，本地状态会丢 */
  open: boolean
  onToggle: () => void
}

function prettyArgs(args: string): string {
  if (!args) return ''
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}

function resultPreview(r: ToolResultInfo): string {
  const s = (r.err || r.text || '').replace(/\s+/g, ' ').trim()
  return s.length > 120 ? s.slice(0, 120) + '…' : s
}

/**
 * ToolCallCard - 工具调用卡片
 */
export function ToolCallCard({ tool, args, accent, running, ended, result, open, onToggle }: ToolCallCardProps) {
  const pretty = prettyArgs(args)
  const hasArgs = !!pretty && pretty !== '{}'
  const failed = !!result?.err
  const success = result && !failed
  const interrupted = !running && !result && !!ended

  return (
    <div
      data-card="tool-call"
      data-running={!!running}
      data-error={failed}
      className="self-start max-w-[90%]"
    >
      {/* 卡片头部：可点击展开/折叠 */}
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        style={accent ? { borderLeftColor: accent } : undefined}
        className={cn(
          'group/tool inline-flex w-full items-center gap-2.5 rounded-lg border-l-2 border-r border-y',
          'bg-surface px-3 py-2 text-sm text-text',
          'transition-colors duration-200',
          failed
            ? 'border-r-status-error border-y-status-error bg-status-error/5 hover:bg-status-error/10'
            : 'border-r-border border-y-border hover:border-r-accent/40 hover:border-y-accent/40 hover:bg-surface-2',
          !accent && 'border-l-accent/30',
        )}
      >
        {/* 展开指示器 */}
        <ChevronRight
          className={cn(
            'h-3.5 w-3.5 flex-shrink-0 text-text-tertiary transition-transform duration-200',
            open && 'rotate-90'
          )}
        />

        {/* 状态指示器 */}
        {running ? (
          <Loader2
            className="h-4 w-4 flex-shrink-0 animate-spin text-accent"
            aria-label="执行中"
          />
        ) : failed ? (
          <XCircle
            className="h-4 w-4 flex-shrink-0 text-status-error"
            aria-label="执行失败"
          />
        ) : success ? (
          <CheckCircle2
            className="h-4 w-4 flex-shrink-0 text-status-success"
            aria-label="执行成功"
          />
        ) : interrupted ? (
          <MinusCircle
            className="h-4 w-4 flex-shrink-0 text-text-tertiary"
            aria-label="已中断"
          />
        ) : (
          <span className="h-2 w-2 flex-shrink-0 rounded-full bg-accent motion-safe:animate-pulse" />
        )}

        {/* 工具信息 */}
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2.5 gap-y-1">
          <span className="flex-shrink-0 text-xs text-text-secondary">调用</span>
          <code className="font-mono text-sm font-semibold text-accent">{tool}</code>

          {/* 耗时 */}
          {!running && result && (
            <span className="flex-shrink-0 text-xs tabular-nums text-text-tertiary">
              {result.durationMs}ms
            </span>
          )}

          {/* 执行中提示 */}
          {running && (
            <span className="flex-shrink-0 text-xs text-text-tertiary">执行中…</span>
          )}
          {interrupted && (
            <span className="flex-shrink-0 text-xs text-text-tertiary">未返回结果（任务结束时在途）</span>
          )}

          {/* 结果预览（折叠时显示）*/}
          {!open && result && (
            <span
              className={cn(
                'min-w-0 flex-1 truncate font-mono text-xs',
                failed ? 'text-status-error' : 'text-text-tertiary'
              )}
            >
              {resultPreview(result)}
            </span>
          )}
        </div>
      </button>

      {/* 展开内容：参数 + 结果 */}
      {open && (
        <div className="mt-2 flex max-w-full flex-col gap-2 animate-slide-down">
          {/* 参数 */}
          {hasArgs && (
            <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
              <div className="border-b border-border px-3 py-1.5">
                <span className="text-xs font-medium text-text-secondary">参数</span>
              </div>
              <pre tabIndex={0} className="max-h-[320px] overflow-auto p-3 text-xs leading-relaxed">
                {pretty}
              </pre>
            </div>
          )}

          {/* 结果 */}
          {result && (
            <div
              className={cn(
                'overflow-hidden rounded-lg border bg-surface-2',
                failed ? 'border-status-error' : 'border-border'
              )}
            >
              <div
                className={cn(
                  'border-b px-3 py-1.5',
                  failed ? 'border-status-error' : 'border-border'
                )}
              >
                <span
                  className={cn(
                    'text-xs font-medium',
                    failed ? 'text-status-error' : 'text-text-secondary'
                  )}
                >
                  {failed ? '错误' : '输出'}
                </span>
              </div>
              <pre tabIndex={0}
                className={cn(
                  'max-h-[320px] overflow-auto p-3 text-xs leading-relaxed',
                  failed && 'text-status-error'
                )}
              >
                {result.err || result.text}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
