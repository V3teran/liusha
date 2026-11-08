import { createColumnHelper } from '@tanstack/react-table'
import type { LLMInvocationSummary } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { TokenPair } from '@/components/TokenPair'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { clockTime, dateOnly, fullTime, humanDuration } from '@/lib/format'
import { invocationStatus } from './llmInvocationStatus'
import { isTimeout, latencyLevel, throughputLabel, type TimingLevel } from './llmTiming'

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
 * 列顺序（日志审计表的通用读法：时间→主体→耗时/token→结论）：
 * # → 时间 → 角色 → 模型 → Tokens → 响应耗时。
 * 正文预览与工具徽章均已裁撤——失败行无正文、正文噪声大于信号、工具消费
 * 面不存在；完整请求与响应原文在详情抽屉按需读。
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
        <span className="block text-right font-mono text-xs tabular-nums text-text-secondary opacity-70">
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
            <em className="block text-xs font-normal not-italic text-text-secondary opacity-75">{clockTime(iso)}</em>
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
            {showProvider && <em className="block truncate text-xs font-normal not-italic text-text-secondary opacity-75">{v.provider}</em>}
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
        // TokenPair：与对话页/任务列同一呈现（↑入↓出 + 箭头；此前是
        // "a / b" 无箭头形态，三面语义不统一）
        return (
          <span className="flex flex-col items-start gap-px whitespace-nowrap">
            <TokenPair inTokens={v.in_tokens} outTokens={v.out_tokens} cachedTokens={v.cached_tokens} className="text-sm" />
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
                <em className="text-xs font-normal not-italic text-text-secondary opacity-75">{throughputLabel(v, llmTimeoutMs)}</em>
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
