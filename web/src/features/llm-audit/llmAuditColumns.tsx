import { createColumnHelper } from '@tanstack/react-table'
import type { LLMInvocationSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { clockTime, dateOnly, fullTime, humanDuration, humanTokens } from '@/lib/format'
import { invocationStatus } from '@/lib/llmInvocationStatus'
import { isTimeout, latencyLevel, throughputLabel, type TimingLevel } from '@/lib/llmTiming'

const columnHelper = createColumnHelper<LLMInvocationSummary>()

/** 表格 meta：# 列页内序号 = startIndex + 行号 + 1（对齐凭证库 # 列口径）。 */
export interface LlmAuditTableMeta {
  readonly startIndex: number
}

const TIMING_DOT: Record<TimingLevel, string> = {
  good: 'bg-sev-low',
  warn: 'bg-sev-medium',
  bad: 'bg-sev-critical',
}

/**
 * 列定义单点声明列宽（对齐 FindingsPage 的 react-table 模式）——不再是列头/骨架屏/数据行
 * 各自一份 `grid-cols-[...]` 字符串手动保持同步，改一次列宽只需改这一处。
 *
 * showProvider：该筛选结果里是否出现过多个 provider，仅此时才在模型列附带展示 provider
 * （单一 provider 时每行重复同一个值是纯噪声）。
 *
 * 列顺序（参考 Datadog/Sentry 日志表）：
 * # → 时间 → 摘要（这次调用干了什么）→ 角色 → 模型 → Tokens → 响应耗时。
 * 摘要是审计表的主读列：正文预览 + 工具徽章，不必逐行点开详情（后端库内派生，不传大字段）。
 * 对话/批次不占列——行→详情抽屉里提供任务/对话跳转按钮（业务标识属于钻取视角，不是筛读视角）。
 */
export function buildLlmAuditColumns(showProvider: boolean, llmTimeoutMs?: number) {
  return [
    columnHelper.display({
      id: 'seq',
      header: '#',
      size: 48,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="block text-right font-mono text-xs tabular-nums text-muted opacity-70">
          {(ctx.table.options.meta as LlmAuditTableMeta).startIndex + ctx.row.index + 1}
        </span>
      ),
    }),
    columnHelper.accessor('created_at', {
      header: '时间',
      size: 104,
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
    columnHelper.display({
      id: 'preview',
      header: '摘要',
      size: 220,
      meta: { headerTitle: '这次调用产出了什么：正文预览 + 请求的工具；点行看完整原文（早期行可能为空）' },
      cell: (ctx) => {
        const v = ctx.row.original
        const tools = v.tool_names ?? []
        if (!v.text_preview && tools.length === 0) {
          return <span className="text-muted opacity-40">—</span>
        }
        return (
          <span className="flex flex-col gap-1 overflow-hidden">
            {v.text_preview && (
              <span className="block truncate text-xs text-text-secondary" title={v.text_preview}>
                {v.text_preview}
              </span>
            )}
            {tools.length > 0 && (
              <span className="flex items-center gap-1 overflow-hidden whitespace-nowrap">
                {tools.slice(0, 3).map((t, i) => (
                  <em
                    key={`${t}-${i}`}
                    className="rounded bg-surface-2 px-1 py-px font-mono text-[10px] not-italic text-muted"
                    title={t}
                  >
                    {t}
                  </em>
                ))}
                {tools.length > 3 && (
                  <em className="text-[10px] not-italic text-muted" title={tools.join(', ')}>
                    +{tools.length - 3}
                  </em>
                )}
              </span>
            )}
          </span>
        )
      },
    }),
    columnHelper.accessor('role', {
      header: '角色',
      size: 88,
      cell: (ctx) => {
        const role = ctx.getValue()
        const { accent } = agentAccent(role)
        return <Badge color={accent}>{agentLabel(role) || role || '—'}</Badge>
      },
    }),
    columnHelper.accessor('model', {
      header: '模型',
      size: 132,
      cell: (ctx) => {
        const v = ctx.row.original
        return (
          <span className="overflow-hidden" title={`${v.model} · ${v.provider}`}>
            <span className="block truncate font-mono text-sm" title={v.model || undefined}>{v.model || '—'}</span>
            {showProvider && <em className="block truncate text-xs font-normal not-italic text-muted opacity-75">{v.provider}</em>}
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'tokens',
      header: 'Tokens',
      size: 120,
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
      size: 136,
      meta: { headerTitle: '模型响应耗时。流式另显首字延迟（TTFT）与输出速率（token/秒）；异常态（失败/超时/截断/内容过滤）内联显示在此列' },
      cell: (ctx) => {
        const v = ctx.row.original
        const timeout = isTimeout(v, llmTimeoutMs)
        const status = invocationStatus(v, llmTimeoutMs)
        // 失败/超时（critical）与截断/过滤（warn）同样值得内联——失败行只有整行红底
        // 无法回答「为什么失败」，且短耗时失败行会按分档落成绿色状态点（误导）。
        const dot = status?.level === 'critical' || timeout ? 'bg-sev-critical' : TIMING_DOT[latencyLevel(v.latency_ms, v.out_tokens)]
        const statusColor = status?.level === 'critical' ? 'text-sev-critical' : 'text-sev-medium'
        return (
          <span className="flex flex-col gap-px whitespace-nowrap text-sm">
            <span className="flex items-center gap-1.5">
              <i className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${dot}`} />
              <span className="font-mono tabular-nums" title={timeout ? '达到 LLM 看门狗上限（5 分钟），该数字非真实响应耗时' : undefined}>
                {humanDuration(v.latency_ms)}
              </span>
              {!timeout && status?.level !== 'critical' && throughputLabel(v, llmTimeoutMs) && (
                <em className="text-xs font-normal not-italic text-muted opacity-75">{throughputLabel(v, llmTimeoutMs)}</em>
              )}
            </span>
            {status && (
              <em className={`block truncate text-xs font-normal not-italic ${statusColor}`} title={status.detail}>
                {status.label}
                {v.error_message ? ` · ${v.error_message}` : ''}
              </em>
            )}
          </span>
        )
      },
    }),
  ]
}
