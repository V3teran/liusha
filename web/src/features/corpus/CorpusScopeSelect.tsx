import { Select } from '@/components/ui/Select'
import { useScenarioOptions } from '@/features/scenarios/useScenarioQueries'

// 知识的适用范围选择框：「通用」+ 全部现存场景。停用的场景也列出（已建任务仍可能在那个场景下
// 检索知识），标注已停用。值 null = 通用；当前值指向的场景已不存在时保留并标注，不悄悄换掉。

interface CorpusScopeSelectProps {
  id?: string
  value: string | null
  onChange: (scenario: string | null) => void
  /** 空值选项的文案：知识的归属叫「通用」，检索视角叫「无场景」 */
  emptyLabel?: string
  disabled?: boolean
  className?: string
  'aria-label'?: string
}

export function CorpusScopeSelect({ id, value, onChange, emptyLabel = '通用', disabled, className, ...rest }: CorpusScopeSelectProps) {
  const q = useScenarioOptions()
  const scenarios = q.data ?? []
  const missing = value !== null && !scenarios.some((s) => s.code === value)
  return (
    <Select
      id={id}
      className={className}
      value={value ?? ''}
      disabled={disabled}
      aria-label={rest['aria-label']}
      onChange={(e) => onChange(e.target.value === '' ? null : e.target.value)}
    >
      <option value="">{emptyLabel}</option>
      {missing && <option value={value}>{q.isPending ? '加载中…' : `${value}（场景已删除）`}</option>}
      {scenarios.map((s) => (
        <option key={s.code} value={s.code}>
          {s.enabled ? s.name : `${s.name}（已停用）`}
        </option>
      ))}
    </Select>
  )
}
