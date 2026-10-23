import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { listTasks } from '@/api/client'
import type { OwnerSummary } from '@/api/types'
import { cn } from '@/lib/utils'

interface OwnerPickerProps {
  value?: string
  onChange: (id: string) => void
}

/**
 * OwnerPicker - 任务/对话选择器
 *
 * 设计改进：
 * 1. 更现代的下拉框样式
 * 2. 刷新按钮图标化
 * 3. 统一圆角和间距
 */
export function OwnerPicker({ value, onChange }: OwnerPickerProps) {
  const [tasks, setTasks] = useState<OwnerSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const list = await listTasks(50)
      setTasks(list)
      if (!value && list.length) onChange(list[0].id)
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载对话失败')
    } finally {
      setLoading(false)
    }
  }, [value, onChange])

  useEffect(() => {
    void load()
  }, [])

  return (
    <div className="flex items-center gap-3">
      <span className="text-sm font-medium text-text-secondary">对话</span>
      <select
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value)}
        disabled={loading}
        className={cn(
          'h-9 w-96 rounded-lg border border-border bg-surface px-3 text-sm text-text',
          'outline-none transition-all disabled:opacity-50 disabled:cursor-not-allowed',
          'focus:border-accent focus:glow-accent'
        )}
      >
        <option value="" disabled>
          选择对话 / 扫描
        </option>
        {tasks.map((s) => (
          <option key={s.id} value={s.id}>
            {s.id.slice(0, 8)} · {s.status}
          </option>
        ))}
      </select>
      <button
        type="button"
        title="刷新对话列表"
        onClick={() => void load()}
        disabled={loading}
        className={cn(
          'flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-surface',
          'text-text-secondary transition-all hover:border-accent hover:text-accent',
          'disabled:opacity-50 disabled:cursor-not-allowed'
        )}
      >
        <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />
      </button>
      {error && <span className="text-xs text-status-error">{error}</span>}
    </div>
  )
}
