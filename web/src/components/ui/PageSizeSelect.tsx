import { PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

interface PageSizeSelectProps {
  value: number
  onChange: (size: number) => void
}

// 每页条数选择器：流量表、漏洞台账共用。紧凑下拉，与分页按钮同行（trailing 槽）。
export function PageSizeSelect({ value, onChange }: PageSizeSelectProps) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      aria-label="每页条数"
      title="每页条数"
      className="h-7 rounded-md border border-border bg-surface px-1.5 text-[12px] text-muted outline-none transition-colors hover:border-border-strong hover:text-text focus:border-accent"
    >
      {PAGE_SIZE_OPTIONS.map((n) => (
        <option key={n} value={n}>
          {n} 条/页
        </option>
      ))}
    </select>
  )
}
