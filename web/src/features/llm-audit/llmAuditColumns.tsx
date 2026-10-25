import { createColumnHelper } from '@tanstack/react-table'
import type { LLMInvocationSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { clockTime, dateOnly, fullTime, humanDuration, humanTokens } from '@/lib/format'
import { invocationStatus } from '@/lib/llmInvocationStatus'
import { isTimeout, latencyLevel, throughputLabel, type TimingLevel } from '@/lib/llmTiming'
import { Link } from 'react-router-dom'

const columnHelper = createColumnHelper<LLMInvocationSummary>()

const TIMING_DOT: Record<TimingLevel, string> = {
  good: 'bg-sev-low',
  warn: 'bg-sev-medium',
  bad: 'bg-sev-critical',
}

/**
 * 列定义单点声明列宽（对齐 FindingsPage 的 react-table 模式）——不再是列头/骨架屏/数据行
 * 各自一份 `grid-cols-[...]` 字符串手动保持同步，改一次列宽只需改这一处。
 *
 * showProvider：该 task 下是否出现过多个 provider，仅此时才在模型列附带展示 provider
 * （单一 provider 时每行重复同一个值是纯噪声，与原实现保持一致）。
 *
 * 业界最佳实践列顺序（参考 Datadog/Sentry）：
 * 1. 时间 - 固定在最左，时序数据的基础维度
 * 2. 对话 ID - 业务标识，用户最关心的上下文
 * 3. 批次 ID - 批量操作入口
 * 4. 角色 - 执行主体
 * 5. 模型 - 资源信息
 * 6. Tokens - 使用量统计
 * 7. 响应耗时 - 性能指标（含状态）
 */
export function buildLlmAuditColumns(showProvider: boolean) {
  return [
    columnHelper.accessor('created_at', {
      header: '时间',
      size: 96,
      cell: (ctx) => {
        const iso = ctx.getValue()
        return (
          <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-xs font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
          </span>
        )
      },
    }),
    columnHelper.accessor('conversation_id', {
      header: '对话',
      size: 100,
      cell: (ctx) => {
        const convId = ctx.getValue()
        if (!convId) return <span className="text-muted opacity-40">—</span>
        const shortId = convId.slice(0, 8)
        return (
          <Link
            to={`/conversations/manual?id=${convId}`}
            className="font-mono text-xs text-accent hover:underline"
            title={`查看对话 ${convId}`}
          >
            {shortId}
          </Link>
        )
      },
    }),
    columnHelper.accessor('assignment_id', {
      header: '批次',
      size: 100,
      cell: (ctx) => {
        const assignId = ctx.getValue()
        if (!assignId) return <span className="text-muted opacity-40">—</span>
        const shortId = assignId.slice(0, 8)
        return (
          <span className="font-mono text-xs text-text" title={`批次 ${assignId}`}>
            {shortId}
          </span>
        )
      },
    }),
    columnHelper.accessor('role', {
      header: '角色',
      size: 96,
      cell: (ctx) => {
        const role = ctx.getValue()
        const { accent } = agentAccent(role)
        return <Badge color={accent}>{agentLabel(role) || role || '—'}</Badge>
      },
    }),
    columnHelper.accessor('model', {
      header: '模型',
      size: 108,
      cell: (ctx) => {
        const v = ctx.row.original
        return (
          <span className="overflow-hidden" title={`${v.model} · ${v.provider}`}>
            <span className="block truncate font-mono text-sm">{v.model || '—'}</span>
            {showProvider && <em className="block truncate text-xs font-normal not-italic text-muted opacity-75">{v.provider}</em>}
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'tokens',
      header: 'Tokens',
      size: 108,
      cell: (ctx) => {
        const v = ctx.row.original
        return (
          <span className="flex flex-col gap-px whitespace-nowrap">
            <span className="font-mono text-sm tabular-nums">
              {humanTokens(v.in_tokens)} / {humanTokens(v.out_tokens)}
            </span>
            {v.cached_tokens > 0 && (
              <em className="block text-xs font-normal not-italic text-muted opacity-75">缓存↓ {humanTokens(v.cached_tokens)}</em>
            )}
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'latency',
      header: '响应耗时',
      size: 124,
      meta: { headerTitle: '模型响应耗时。流式另显首字延迟（TTFT）与输出速率（token/秒）；异常态（超时/截断/内容过滤）内联显示在此列' },
      cell: (ctx) => {
        const v = ctx.row.original
        const timeout = isTimeout(v)
        const status = invocationStatus(v)
        const warnStatus = status?.level === 'warn' ? status : null
        return (
          <span className="flex flex-col gap-px whitespace-nowrap text-sm">
            <span className="flex items-center gap-1.5">
              <i className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${timeout ? 'bg-sev-critical' : TIMING_DOT[latencyLevel(v.latency_ms, v.out_tokens)]}`} />
              <span className="font-mono tabular-nums" title={timeout ? '达到 LLM 看门狗上限（5 分钟），该数字非真实响应耗时' : undefined}>
                {humanDuration(v.latency_ms)}
              </span>
              {!timeout && throughputLabel(v) && <em className="text-xs font-normal not-italic text-muted opacity-75">{throughputLabel(v)}</em>}
            </span>
            {warnStatus && (
              <em className="flex items-center gap-1 text-xs font-normal not-italic text-sev-medium" title={warnStatus.detail}>
                <i className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-sev-medium" />
                {warnStatus.label}
              </em>
            )}
          </span>
        )
      },
    }),
  ]
}
