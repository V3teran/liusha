interface StatChipProps {
  color: string
  label: string
  value: string
  title?: string
}

/**
 * StatChip - 统计芯片组件
 *
 * 业界最佳实践（参考 Datadog/Grafana）：
 * 1. 彩色左边框而非小圆点（更醒目）
 * 2. 标签小灰字，数值大黑字（视觉层次）
 * 3. 紧凑布局，悬停轻微提升
 */
export function StatChip({ color, label, value, title }: StatChipProps) {
  return (
    <span
      className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface pl-2 pr-2.5 text-xs transition-all hover:border-border-strong hover:shadow-sm"
      style={{ borderLeftWidth: '3px', borderLeftColor: color }}
      title={title}
    >
      <span className="text-muted">{label}</span>
      <b className="font-mono text-sm font-semibold tabular-nums text-text">{value}</b>
    </span>
  )
}
