import { SearchInput } from '@/components/SearchInput'
import { useState, useEffect } from 'react'

interface LlmAuditSearchBarProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  /** 布局类（宽度），由所在筛选栏决定 */
  className?: string
}

/**
 * LlmAuditSearchBar - 模型日志搜索栏
 *
 * 支持搜索：
 * - 对话 ID (conversation_id)
 * - 批次 ID (assignment_id)
 *
 * 实时搜索，带 debounce 防抖
 */
export function LlmAuditSearchBar({ value, onChange, placeholder = '搜索对话、批次 ID…', className = 'w-64' }: LlmAuditSearchBarProps) {
  const [localValue, setLocalValue] = useState(value)

  // 同步外部变化
  useEffect(() => {
    setLocalValue(value)
  }, [value])

  // Debounce：300ms 后触发搜索
  useEffect(() => {
    const timer = setTimeout(() => {
      if (localValue !== value) {
        onChange(localValue)
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [localValue, value, onChange])

  return (
    <SearchInput
      className={className}
      value={localValue}
      onChange={setLocalValue}
      placeholder={placeholder}
    />
  )
}
