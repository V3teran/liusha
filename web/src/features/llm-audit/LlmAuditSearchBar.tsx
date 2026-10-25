import { Search, X } from 'lucide-react'
import { useState, useEffect, useCallback } from 'react'

interface LlmAuditSearchBarProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
}

/**
 * LlmAuditSearchBar - LLM 审计搜索栏
 *
 * 支持搜索：
 * - 对话 ID (conversation_id)
 * - 批次 ID (assignment_id)
 *
 * 实时搜索，带 debounce 防抖
 */
export function LlmAuditSearchBar({ value, onChange, placeholder = '搜索对话、批次 ID...' }: LlmAuditSearchBarProps) {
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

  const handleClear = useCallback(() => {
    setLocalValue('')
    onChange('')
  }, [onChange])

  return (
    <div className="relative flex-1 max-w-md">
      <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
      <input
        type="search"
        value={localValue}
        onChange={(e) => setLocalValue(e.target.value)}
        placeholder={placeholder}
        className="w-full rounded-lg border border-border bg-surface py-2 pl-10 pr-10 text-sm text-text outline-none transition-colors placeholder:text-muted focus:border-accent focus:ring-1 focus:ring-accent"
      />
      {localValue && (
        <button
          type="button"
          onClick={handleClear}
          className="absolute right-3 top-1/2 -translate-y-1/2 text-muted hover:text-text"
          aria-label="清空搜索"
        >
          <X className="h-4 w-4" />
        </button>
      )}
    </div>
  )
}
