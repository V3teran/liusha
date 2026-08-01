import { useCallback, useEffect, useState } from 'react'
import { listTasks } from '@/api/client'
import type { OwnerSummary } from '@/api/types'

interface OwnerPickerProps {
  value?: string
  onChange: (id: string) => void
  modeFilter?: string
}

// owner 选择器：拉最近会话/扫描列表，下拉选一个 owner_id。数据页共用。
// 可选 modeFilter 只显示某模式（active/passive）。挂载时自动选第一个。
export function OwnerPicker({ value, onChange, modeFilter }: OwnerPickerProps) {
  const [tasks, setTasks] = useState<OwnerSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      let list = await listTasks(50)
      if (modeFilter) list = list.filter((s) => s.mode === modeFilter)
      setTasks(list)
      if (!value && list.length) onChange(list[0].id)
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载对话失败')
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modeFilter])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="flex items-center gap-2.5">
      <span className="text-[13px] text-muted">对话</span>
      <select
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value)}
        disabled={loading}
        className="w-[360px] rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent disabled:opacity-60"
      >
        <option value="" disabled>
          选择对话 / 扫描
        </option>
        {tasks.map((s) => (
          <option key={s.id} value={s.id}>
            {s.mode || '?'} · {s.id.slice(0, 8)} · {s.status}
          </option>
        ))}
      </select>
      <button
        type="button"
        title="刷新对话列表"
        onClick={() => void load()}
        className="h-[30px] w-[30px] rounded-lg border border-border bg-surface text-muted hover:border-accent hover:text-accent"
      >
        ↻
      </button>
      {error && <span className="text-xs text-sev-critical">{error}</span>}
    </div>
  )
}
