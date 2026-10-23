interface StatChipProps {
  color: string
  label: string
  value: string
  title?: string
}

/**
 * StatChip - 统计芯片组件
 *
 * 设计改进：
 * 1. 更精致的样式（圆角、间距）
 * 2. 更清晰的视觉层次
 * 3. 悬停状态反馈
 */
export function StatChip({ color, label, value, title }: StatChipProps) {
  return (
    <span
      className="inline-flex h-8 items-center gap-2 rounded-lg border border-border bg-surface px-3 text-xs transition-all hover:bg-surface-2"
      title={title}
    >
      <i className="h-4 w-1 flex-shrink-0 rounded-full" style={{ background: color }} />
      <span className="text-text-secondary">{label}</span>
      <b className="font-mono font-semibold tabular-nums text-text">{value}</b>
    </span>
  )
}
