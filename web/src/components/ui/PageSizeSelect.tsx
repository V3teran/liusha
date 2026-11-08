import { PAGE_SIZE_OPTIONS } from '@/lib/pageSize'
import { Select } from './Select'

interface PageSizeSelectProps {
  value: number
  onChange: (size: number) => void
  className?: string
}

// 每页条数选择器：分页条内的紧凑下拉（sm 档，与翻页按钮同高）。
export function PageSizeSelect({ value, onChange, className }: PageSizeSelectProps) {
  return (
    <Select
      size="sm"
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      aria-label="每页条数"
      title="每页条数"
      className={className}
    >
      {PAGE_SIZE_OPTIONS.map((n) => (
        <option key={n} value={n}>
          {n} 条/页
        </option>
      ))}
    </Select>
  )
}
