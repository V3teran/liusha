import { useEffect, useState } from 'react'
import { listScenarios } from '@/api/client'
import type { Scenario } from '@/api/types'

interface ScenarioPickerProps {
  value?: string
  onChange: (code: string) => void
}

// 场景选择：挂载时拉 GET /scenarios（仅 enabled），value 用 scenario.code（业务主键，非 uuid），
// 默认选第一个。不再按 mode 过滤——场景已由后端统一枚举，来源/引擎与场景选择正交。
export function ScenarioPicker({ value, onChange }: ScenarioPickerProps) {
  const [scenarios, setScenarios] = useState<Scenario[]>([])

  useEffect(() => {
    let mounted = true
    void listScenarios().then((ss) => {
      if (!mounted) return
      setScenarios(ss)
      if (!value && ss[0]) onChange(ss[0].code)
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
        <option key={s.code} value={s.code}>
          {s.name}
        </option>
      ))}
    </select>
  )
}
