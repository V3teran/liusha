import { OwnerPicker } from '@/components/OwnerPicker'
import { StatChip } from '@/components/StatChip'
import type { LLMInvocationStat } from '@/api/types'
import { compactNumber, humanDuration, humanTokens } from '@/lib/format'

interface LlmAuditToolbarProps {
  taskId: string
  onTaskIdChange: (id: string) => void
  stat?: LLMInvocationStat
}

// 工具栏：会话选择 + 统计 chip（调用数/输入/输出/缓存/耗时）。
export function LlmAuditToolbar({ taskId, onTaskIdChange, stat }: LlmAuditToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-4.5 border-b border-border px-5.5 py-3">
      <OwnerPicker value={taskId} onChange={onTaskIdChange} />
      {stat && (
        <div className="flex flex-wrap items-center gap-2">
          <StatChip color="var(--accent)" label="调用" value={humanTokens(stat.calls)} />
          <StatChip color="#38bdf8" label="输入" value={compactNumber(stat.in_tokens)} title={humanTokens(stat.in_tokens)} />
          <StatChip color="#34d399" label="输出" value={compactNumber(stat.out_tokens)} title={humanTokens(stat.out_tokens)} />
          <StatChip color="#a78bfa" label="缓存" value={compactNumber(stat.cached_tokens)} title={humanTokens(stat.cached_tokens)} />
          <StatChip color="#94a3b8" label="耗时" value={humanDuration(stat.latency_ms)} />
        </div>
      )}
    </div>
  )
}
