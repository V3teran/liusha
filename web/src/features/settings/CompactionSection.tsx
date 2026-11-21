import type { ReactNode } from 'react'
import { Field } from '@/features/config/ConfigDrawer'
import { NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { SettingsSection } from './SettingsSection'
import { useSettingSection } from './useSettingSection'
import { SettingStatus, sectionProps } from './sectionBindings'

export function CompactionSection() {
  const s = useSettingSection('compaction')
  const d = s.draft
  const invalid =
    !d ||
    d.planner_context_ratio <= 0 ||
    d.executor_context_ratio <= 0 ||
    d.evaluator_context_ratio <= 0 ||
    d.monitor_context_ratio <= 0 ||
    d.planner_context_ratio > 0.9 ||
    d.executor_context_ratio > 0.9 ||
    d.evaluator_context_ratio > 0.9 ||
    d.monitor_context_ratio > 0.9

  return (
    <SettingsSection
      title="上下文压缩"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <RatioField
            label="规划者历史预算"
            aside={<SettingStatus s={s} field="planner_context_ratio" />}
            value={d.planner_context_ratio}
            onChange={(v) => s.patch({ planner_context_ratio: v })}
          />
          <RatioField
            label="执行者历史预算"
            aside={<SettingStatus s={s} field="executor_context_ratio" />}
            value={d.executor_context_ratio}
            onChange={(v) => s.patch({ executor_context_ratio: v })}
          />
          <RatioField
            label="评估者历史预算"
            aside={<SettingStatus s={s} field="evaluator_context_ratio" />}
            value={d.evaluator_context_ratio}
            onChange={(v) => s.patch({ evaluator_context_ratio: v })}
          />
          <RatioField
            label="监察者历史预算"
            aside={<SettingStatus s={s} field="monitor_context_ratio" />}
            value={d.monitor_context_ratio}
            onChange={(v) => s.patch({ monitor_context_ratio: v })}
          />
        </>
      )}
    </SettingsSection>
  )
}

// RatioField 是历史预算输入：值是占该角色路由模型上下文窗口的比例（0 < 比例 ≤ 0.9，步进 0.05），
// 单位「× 上下文窗口」紧跟在数值后，与其它字段的（秒）（字节）同理——含义由单位表达，不另写说明。
function RatioField({
  label,
  aside,
  value,
  onChange,
}: {
  label: string
  aside: ReactNode
  value: number
  onChange: (v: number) => void
}) {
  return (
    <Field label={label} aside={aside}>
      <span className="flex items-center gap-2">
        <input
          type="number"
          min={0.05}
          max={0.9}
          step={0.05}
          className={NUMBER_INPUT_CLASS}
          value={value}
          onChange={(e) => onChange(Number(e.target.value))}
        />
        <span className="flex-shrink-0 text-sm text-text-secondary">× 上下文窗口</span>
      </span>
    </Field>
  )
}
