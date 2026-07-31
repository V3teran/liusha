import { createColumnHelper } from '@tanstack/react-table'
import { Settings2 } from 'lucide-react'
import type { LLMInvocationSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { clockTime, dateOnly, fullTime, humanDuration, humanTokens } from '@/lib/format'
import { invocationStatus } from '@/lib/llmInvocationStatus'
import { isTimeout, latencyLevel, throughputLabel, ttftLevel, type TimingLevel } from '@/lib/llmTiming'

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
 * 没有独立的「状态」列：绝大多数调用都是正常完成，独立一列会导致几乎每行都显示"—"，
 * 挤占「内容」列的空间却没有信息量。异常态（失败/超时/截断/内容过滤）改为并入已有列：
 * 失败/超时在「响应耗时」格内联出现（原实现已如此），截断/内容过滤同样内联在该格；
 * 失败行整行标红底色（LlmAuditTable 的 data-error），一眼可扫，不需要额外一列。
 */
export function buildLlmAuditColumns(showProvider: boolean) {
  return [
    columnHelper.accessor('created_at', {
      header: '时间',
      size: 96, // 相对权重（非像素）——渲染时经 columnWidthPercents 换算成百分比，随容器宽度缩放
      cell: (ctx) => {
        const iso = ctx.getValue()
        return (
          <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-[10.5px] font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
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
            <span className="block truncate font-mono text-[12.5px]">{v.model || '—'}</span>
            {showProvider && <em className="block truncate text-[10.5px] font-normal not-italic text-muted opacity-75">{v.provider}</em>}
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
            <span className="font-mono text-[12.5px] tabular-nums">
              {humanTokens(v.in_tokens)} / {humanTokens(v.out_tokens)}
            </span>
            {v.cached_tokens > 0 && (
              <em className="block text-[10.5px] font-normal not-italic text-muted opacity-75">缓存↓ {humanTokens(v.cached_tokens)}</em>
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
        // 截断/内容过滤（warn 级）没有失败也没有超时，需要单独提示，否则容易被忽略
        // ——它们不影响本次调用是否算「成功」，但输出可能不完整/被拦截。
        const status = invocationStatus(v)
        const warnStatus = status?.level === 'warn' ? status : null
        return (
          <span className="flex flex-col gap-px whitespace-nowrap text-[12.5px]">
            {v.is_stream && v.ttft_ms > 0 && (
              <em className="flex items-center gap-1.5 text-[10.5px] font-normal not-italic text-muted opacity-75">
                <i className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${TIMING_DOT[ttftLevel(v.ttft_ms)]}`} />
                首字 {humanDuration(v.ttft_ms)}
              </em>
            )}
            <span className="flex items-center gap-1.5">
              <i className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${timeout ? 'bg-sev-critical' : TIMING_DOT[latencyLevel(v.latency_ms, v.out_tokens)]}`} />
              <span className="font-mono tabular-nums" title={timeout ? '达到 LLM 看门狗上限（5 分钟），该数字非真实响应耗时' : undefined}>
                {humanDuration(v.latency_ms)}
              </span>
              {!timeout && throughputLabel(v) && <em className="text-[10.5px] font-normal not-italic text-muted opacity-75">{throughputLabel(v)}</em>}
            </span>
            {warnStatus && (
              <em className="flex items-center gap-1 text-[10.5px] font-normal not-italic text-sev-medium" title={warnStatus.detail}>
                <i className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-sev-medium" />
                {warnStatus.label}
              </em>
            )}
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'content',
      header: () => <span title="这次调用产出了什么：请求的工具名（徽章）或文本回复预览">内容</span>,
      size: 220, // 「内容」列权重最大——占比例布局里的剩余大头空间，同一权重体系下不再需要 0 这种特殊值
      cell: (ctx) => {
        const v = ctx.row.original
        if (v.tool_names.length > 0) {
          return (
            <span className="flex items-center gap-1.5 overflow-hidden" title={`调用了 ${v.tool_names.length} 个工具：${v.tool_names.join('、')}`}>
              <Settings2 className="h-3 w-3 flex-shrink-0 text-muted opacity-70" aria-hidden="true" />
              {v.tool_names.map((t, i) => (
                <Badge key={`${v.id}-${i}`} dot={false} color="var(--accent)" className="flex-shrink-0">
                  {t}
                </Badge>
              ))}
            </span>
          )
        }
        if (v.text_preview) {
          return (
            <span className="block overflow-hidden truncate text-xs text-muted" title={v.text_preview}>
              {v.text_preview}
            </span>
          )
        }
        return <span className="text-muted opacity-40">—</span>
      },
    }),
  ]
}
