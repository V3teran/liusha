import { cn } from '@/lib/utils'

/**
 * TokenPair：输入/输出 token 的统一呈现——↑输入 ↓输出、tabular-nums 右对齐、
 * 悬停精确值。token 是中性度量数据（不是告警/风险），不借用漏洞严重度色板
 * （曾用 sev-info/sev-high 把「输出」染成高危红，语义错位）；对话页、任务
 * 列表、LLM 审计三面共用，一处定终身。
 */
export function TokenPair({
  inTokens,
  outTokens,
  cachedTokens,
  className,
}: {
  inTokens: number
  outTokens: number
  cachedTokens?: number
  className?: string
}) {
  const compact = (n: number) =>
    n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1_000 ? `${(n / 1_000).toFixed(1)}k` : String(n)
  const title = [`输入 ${inTokens.toLocaleString()}`, `输出 ${outTokens.toLocaleString()}`]
  if (cachedTokens && cachedTokens > 0) title.push(`缓存命中 ${cachedTokens.toLocaleString()}`)
  return (
    <span
      className={cn('whitespace-nowrap font-mono text-xs tabular-nums text-text', className)}
      title={title.join(' · ')}
    >
      <span title="输入">↑{compact(inTokens)}</span>{' '}
      <span title="输出">↓{compact(outTokens)}</span>
    </span>
  )
}
