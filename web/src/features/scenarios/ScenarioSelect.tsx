import type { ScenarioListItem } from '@/api/types'
import { Select } from '@/components/ui/Select'
import type { ControlSize } from '@/components/ui/control'
import { useScenarioOptions } from './useScenarioQueries'

// 场景选择框：下发入口（新建任务含定时、对话发起）共用。只列启用的场景，缺省场景排第一，
// 末位「无场景」；缺省场景被停用或删除时，未选即显示「无场景」。
//
// 值的三种状态与请求字段 scenario 一一对应：null = 未选（不带字段，由服务端落定缺省场景，
// 框里显示缺省场景）；'' = 无场景；其余为场景 code。缺省是谁只有服务端说了算（is_default），
// 前端不另记一份。

/** 无场景的选项值。 */
export const NO_SCENARIO = ''

/** 未选且缺省场景尚不可知时的占位选项值（不会作为场景提交）。 */
const UNSET = '\u0000unset'

interface ScenarioSelectProps {
  id?: string
  value: string | null
  onChange: (code: string) => void
  disabled?: boolean
  size?: ControlSize
  className?: string
  'aria-label'?: string
}

/** 可选的场景：启用的场景，缺省场景排第一。 */
export function selectableScenarios(all: ScenarioListItem[]): ScenarioListItem[] {
  const enabled = all.filter((s) => s.enabled)
  return [...enabled.filter((s) => s.is_default), ...enabled.filter((s) => !s.is_default)]
}

export function ScenarioSelect({ id, value, onChange, disabled, size, className, ...rest }: ScenarioSelectProps) {
  const q = useScenarioOptions()
  const options = selectableScenarios(q.data ?? [])
  const defaultCode = options.find((s) => s.is_default)?.code
  // 未选（null）：显示缺省场景；缺省场景被停用或删除时缺省即无场景（与服务端同一口径）。列表
  // 未到或加载失败时缺省是谁还不知道，如实显示，提交时不带字段、由服务端落定。
  const shown = value ?? defaultCode ?? (q.isSuccess ? NO_SCENARIO : UNSET)
  // 选中的场景不在可选项里：列表未到、加载失败，或它已被停用 / 删除。保留并如实标注，不悄悄
  // 换成别的场景——提交时服务端会给出不可用的原因。
  const missing = shown !== UNSET && shown !== NO_SCENARIO && !options.some((s) => s.code === shown)
  const pendingLabel = q.isPending ? '加载中…' : q.isError ? '场景列表加载失败' : null
  return (
    <Select
      id={id}
      size={size}
      className={className}
      value={shown}
      disabled={disabled}
      aria-label={rest['aria-label']}
      onChange={(e) => e.target.value !== UNSET && onChange(e.target.value)}
    >
      {shown === UNSET && <option value={UNSET}>{pendingLabel ?? '缺省场景'}</option>}
      {missing && <option value={shown}>{pendingLabel ?? `${shown}（已不可用）`}</option>}
      {options.map((s) => (
        <option key={s.code} value={s.code} title={s.description}>
          {s.name}
        </option>
      ))}
      <option value={NO_SCENARIO}>无场景</option>
    </Select>
  )
}
