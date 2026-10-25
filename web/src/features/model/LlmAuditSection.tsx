import { useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { Link } from 'react-router-dom'
import { OwnerPicker } from '@/components/OwnerPicker'
import { useLLMInvocationStat } from './useModelQueries'

// LLM 审计区块：快速统计面板，展示选定对话的 LLM 调用汇总，
// 链接到完整审计页查看详情。放置在 LLM 配置页顶部作为监控入口。
export function LlmAuditSection() {
  const [taskId, setTaskId] = useState<string>()
  const { data: stat, isLoading } = useLLMInvocationStat(taskId)

  return (
    <section aria-label="LLM 审计概览" className="overflow-hidden rounded-lg border border-border bg-surface">
      <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text">LLM 审计</h2>
          <p className="mt-0.5 text-xs text-muted">监控模型调用统计和成本</p>
        </div>
        <Link
          to="/llm-audit"
          className="flex items-center gap-1.5 rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text transition-colors hover:border-accent hover:text-accent"
        >
          查看详情
          <ExternalLink className="h-3.5 w-3.5" />
        </Link>
      </div>

      <div className="flex flex-col gap-4 p-4">
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted">选择对话：</span>
          <div className="flex-1">
            <OwnerPicker value={taskId} onChange={setTaskId} />
          </div>
        </div>

        {!taskId ? (
          <div className="py-6 text-center text-sm text-muted">
            请选择一个对话查看 LLM 调用统计
          </div>
        ) : isLoading ? (
          <div className="tac-cursor py-6 text-center font-mono text-sm text-muted">加载中</div>
        ) : !stat ? (
          <div className="py-6 text-center text-sm text-muted">无调用数据</div>
        ) : (
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <StatCard label="调用次数" value={stat.calls.toLocaleString()} />
            <StatCard label="输入 Tokens" value={stat.in_tokens.toLocaleString()} />
            <StatCard label="输出 Tokens" value={stat.out_tokens.toLocaleString()} />
            <StatCard
              label="平均延迟"
              value={`${Math.round(stat.latency_ms / stat.calls)}ms`}
            />
          </div>
        )}
      </div>
    </section>
  )
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg bg-surface-2 px-3 py-2">
      <span className="text-xs text-muted">{label}</span>
      <span className="font-mono text-lg font-semibold text-text">{value}</span>
    </div>
  )
}
