import type { ReactNode } from 'react'
import { Field } from '@/features/config/ConfigDrawer'
import { INPUT_CLASS } from '@/components/ui/control'
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
      subtitle="各角色 ReAct 循环的历史 token 预算 = 占比 × 该角色路由模型的 context_window；超预算的前缀被裁并蒸馏成纪要，占比越大上下文越完整、token 越贵"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <RatioField
            label="规划占比"
            aside={<SettingStatus s={s} field="planner_context_ratio" />}
            hint="规划链路长、上下文价值高，占比最大"
            value={d.planner_context_ratio}
            onChange={(v) => s.patch({ planner_context_ratio: v })}
          />
          <RatioField
            label="执行占比"
            aside={<SettingStatus s={s} field="executor_context_ratio" />}
            hint="执行工具循环的中档预算"
            value={d.executor_context_ratio}
            onChange={(v) => s.patch({ executor_context_ratio: v })}
          />
          <RatioField
            label="裁决占比"
            aside={<SettingStatus s={s} field="evaluator_context_ratio" />}
            hint="单假设取证链路短，预算最小"
            value={d.evaluator_context_ratio}
            onChange={(v) => s.patch({ evaluator_context_ratio: v })}
          />
          <RatioField
            label="监察占比"
            aside={<SettingStatus s={s} field="monitor_context_ratio" />}
            hint="周期性状态评估，链路最短"
            value={d.monitor_context_ratio}
            onChange={(v) => s.patch({ monitor_context_ratio: v })}
          />
        </>
      )}
    </SettingsSection>
  )
}

// RatioField 是历史预算占比输入（0 < 比例 ≤ 0.9，步进 0.05）。
function RatioField({
  label,
  hint,
  aside,
  value,
  onChange,
}: {
  label: string
  hint: string
  aside: ReactNode
  value: number
  onChange: (v: number) => void
}) {
  return (
    <Field label={label} hint={`${hint}，>0 且 ≤0.9`} aside={aside}>
      <input
        type="number"
        min={0.05}
        max={0.9}
        step={0.05}
        className={INPUT_CLASS}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
    </Field>
  )
}
