import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cn } from '@/lib/utils'

interface SpawnCardProps {
  args?: string
  durationMs?: number // 完成时：子代理执行总耗时
  err?: string // 完成时：子代理出错信息
  done?: boolean // true=派发完成卡
}

interface SpawnArgs {
  subagent_type?: string
  description?: string
}

function parseArgs(args?: string): SpawnArgs {
  try {
    return JSON.parse(args || '{}')
  } catch {
    return {}
  }
}

const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

// 派发卡：orchestrator 派子代理（AI 指挥 AI 团队）。两个时刻：
//   - 派发开始（args）：🛰️ 派发 → reconnaissance + brief
//   - 派发完成（done + durationMs）：✓/✗ + 子代理执行总时长（task 工具的 tool_result）
export function SpawnCard({ args, durationMs, err, done }: SpawnCardProps) {
  const parsed = parseArgs(args)
  const agentColor = agentAccent(parsed.subagent_type)
  const agentText = agentLabel(parsed.subagent_type) || '子代理'
  const brief = (parsed.description || '').trim()

  return (
    <div
      data-card="spawn"
      className={cn(
        'self-start max-w-[88%] rounded-xl border border-border border-l-[3px] px-3.5 py-2.5 shadow-sm',
        done ? 'bg-surface' : 'bg-gradient-to-b from-violet-400/10 to-transparent',
        err ? 'border-l-sev-critical' : 'border-l-violet-400',
      )}
    >
      <div className="flex items-center gap-1.5 text-[13px]">
        {done ? (
          <>
            <span className={cn('text-sm', err ? 'text-sev-critical' : 'text-emerald-500')}>{err ? '✗' : '✓'}</span>
            <span className="text-xs font-semibold text-violet-400">派发{err ? '失败' : '完成'}</span>
            {!!durationMs && (
              <span
                className="ml-auto rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted"
                title="子代理执行总耗时"
              >
                ⏱ {fmtMs(durationMs)}
              </span>
            )}
          </>
        ) : (
          <>
            <span className="text-sm">🛰️</span>
            <span className="text-xs font-semibold text-violet-400">派发</span>
            <span className="text-muted">→</span>
            <span
              className="rounded px-2 py-0.5 font-mono font-semibold"
              style={{ color: agentColor.accent, background: agentColor.soft }}
            >
              {agentText}
            </span>
          </>
        )}
      </div>
      {!done && brief && (
        <div className="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{brief}</div>
      )}
      {done && err && (
        <div className="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-sev-critical">{err}</div>
      )}
    </div>
  )
}
