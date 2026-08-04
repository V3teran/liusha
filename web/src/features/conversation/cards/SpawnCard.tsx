import { Check, X } from 'lucide-react'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { CardHeader } from './CardHeader'

interface SpawnCardProps {
  dispatcher?: string // 派发者 hunter（编排）——领头
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

// 派发正文（方案 B）：编排 hunter 派子 hunter（AI 指挥 AI 团队）。外层 AgentCard 承载图标。
// 领头「编排 派发 → 侦察」：编排(施动)领头，动作弱化，目标 hunter 以其配色收尾——主语不再漂移。
//   - 派发开始（args）：编排 派发 → 侦察 + brief
//   - 派发完成（done + durationMs）：编排 派发完成/失败 + ✓/✗ + 子代理执行总时长
export function SpawnCard({ dispatcher, args, durationMs, err, done }: SpawnCardProps) {
  const parsed = parseArgs(args)
  const target = agentAccent(parsed.subagent_type)
  const targetText = agentLabel(parsed.subagent_type) || '子代理'
  const brief = (parsed.description || '').trim()
  const disp = agentAccent(dispatcher)
  const dispText = agentLabel(dispatcher) || '编排'

  if (done) {
    return (
      <div data-card="spawn">
        <CardHeader
          hunter={dispText}
          hunterColor={disp.accent}
          action={err ? '派发失败' : '派发完成'}
          extras={
            <>
              {err ? (
                <X className="h-3.5 w-3.5 text-sev-critical" strokeWidth={2.5} aria-hidden="true" />
              ) : (
                <Check className="h-3.5 w-3.5 text-emerald-500" strokeWidth={2.5} aria-hidden="true" />
              )}
              {!!durationMs && (
                <span
                  className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted"
                  title="子代理执行总耗时"
                >
                  ⏱ {fmtMs(durationMs)}
                </span>
              )}
            </>
          }
        />
        {err && <div className="whitespace-pre-wrap break-words text-sm leading-relaxed text-sev-critical">{err}</div>}
      </div>
    )
  }

  return (
    <div data-card="spawn">
      <CardHeader
        hunter={dispText}
        hunterColor={disp.accent}
        action="派发"
        target={{ name: targetText, color: target.accent }}
      />
      {brief && <div className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{brief}</div>}
    </div>
  )
}
