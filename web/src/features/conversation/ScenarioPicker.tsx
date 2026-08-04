import { useEffect, useState } from 'react'
import { listScenarios } from '@/api/client'
import type { ScenarioConfig } from '@/api/types'

interface ScenarioPickerProps {
  value?: string
  onChange: (code: string) => void
}

// 场景选择：挂载时拉 GET /scenarios（全量，含停用），value 用 scenario.code（业务主键，非 uuid）。
// 停用场景照常展示但 option 置灰不可选（可见 ≠ 可用）；默认自动选中第一个「启用」场景。
export function ScenarioPicker({ value, onChange }: ScenarioPickerProps) {
  const [scenarios, setScenarios] = useState<ScenarioConfig[]>([])

  useEffect(() => {
    let mounted = true
    void listScenarios().then((ss) => {
      if (!mounted) return
      setScenarios(ss)
      const firstEnabled = ss.find((s) => s.enabled)
      if (!value && firstEnabled) onChange(firstEnabled.code)
    })
    return () => {
      mounted = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <select
      value={value ?? ''}
      onChange={(e) => onChange(e.target.value)}
      className="w-60 rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
    >
      <option value="" disabled>
        选择场景
      </option>
      {scenarios.map((s) => (
        <option key={s.code} value={s.code} disabled={!s.enabled}>
          {s.name}
          {s.enabled ? '' : '（已停用）'}
        </option>
      ))}
    </select>
  )
}
