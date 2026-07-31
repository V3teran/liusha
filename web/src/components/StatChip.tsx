interface StatChipProps {
  color: string
  label: string
  value: string
  title?: string
}

// 工具栏统计 chip：色条 + label + 数值。LlmAuditPage 里原本 5 个几乎相同的手写 <span>，
// 只有颜色/文案/数值不同——抽成一个组件，新增一个统计维度只需加一行调用。
export function StatChip({ color, label, value, title }: StatChipProps) {
  return (
    <span
      className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-border bg-surface px-2.5 text-xs text-muted"
      title={title}
    >
      <i className="h-3.5 w-0.5 flex-shrink-0 rounded-full" style={{ background: color }} />
      {label}
      <b className="font-mono font-semibold tabular-nums text-text">{value}</b>
    </span>
  )
}
