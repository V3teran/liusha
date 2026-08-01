import { Check, X } from 'lucide-react'
import { agentAccent, agentLabel } from '@/lib/agentColor'

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

// 派发文字流（方案 A）：orchestrator 派子代理（AI 指挥 AI 团队）。无卡片外壳/渐变，
// 外层 RailNode 图标点承载身份。两个时刻：
//   - 派发开始（args）：派发 → reconnaissance + brief
//   - 派发完成（done + durationMs）：✓/✗（lucide）+ 子代理执行总时长
export function SpawnCard({ args, durationMs, err, done }: SpawnCardProps) {
  const parsed = parseArgs(args)
  const agentColor = agentAccent(parsed.subagent_type)
  const agentText = agentLabel(parsed.subagent_type) || '子代理'
  const brief = (parsed.description || '').trim()

  return (
    <div data-card="spawn">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {done ? (
          <>
            {err ? (
              <X className="h-3.5 w-3.5 text-sev-critical" strokeWidth={2.5} aria-hidden="true" />
            ) : (
              <Check className="h-3.5 w-3.5 text-emerald-500" strokeWidth={2.5} aria-hidden="true" />
            )}
            <span className="font-semibold text-violet-400">派发{err ? '失败' : '完成'}</span>
            {!!durationMs && (
              <span
                className="ml-auto rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted"
                title="子代理执行总耗时"
              >
                ⏱ {fmtMs(durationMs)}
              </span>
            )}
          </>
        ) : (
          <>
            <span className="font-semibold text-violet-400">派发</span>
            <span className="text-muted">→</span>
            <span
              className="rounded-md px-2 py-0.5 font-mono font-semibold"
              style={{ color: agentColor.accent, background: agentColor.soft }}
            >
              {agentText}
            </span>
          </>
        )}
      </div>
      {!done && brief && (
        <div className="mt-1.5 whitespace-pre-wrap break-words text-[13px] leading-relaxed text-muted">{brief}</div>
      )}
      {done && err && (
        <div className="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-sev-critical">{err}</div>
      )}
    </div>
  )
}
