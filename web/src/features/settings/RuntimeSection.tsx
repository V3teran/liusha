import { useQueryClient } from '@tanstack/react-query'
import { Field } from '@/features/config/ConfigDrawer'
import { NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { SettingsSection } from './SettingsSection'
import { useSettingSection } from './useSettingSection'
import { SettingStatus, sectionProps } from './sectionBindings'

export function RuntimeSection() {
  const queryClient = useQueryClient()
  // onSaved 失效 LLM 审计页的 runtime 缓存（useRuntimeSettings 同 key）：
  // 看门狗秒数改完，审计页的超时判定/展示立即可见，不再等 60s staleTime。
  const s = useSettingSection('runtime', () => {
    void queryClient.invalidateQueries({ queryKey: ['settings', 'runtime'] })
  })
  const d = s.draft
  const invalid =
    !d ||
    d.step_tool_timeout_seconds <= 0 ||
    d.step_llm_timeout_seconds <= 0 ||
    d.run_tail_bytes <= 0 ||
    d.tool_output_tail_bytes <= 0

  return (
    <SettingsSection
      title="工具运行时"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <Field label="工具超时钳制（秒）" aside={<SettingStatus s={s} field="step_tool_timeout_seconds" />}>
            <input
              type="number"
              min={1}
              className={NUMBER_INPUT_CLASS}
              value={d.step_tool_timeout_seconds}
              onChange={(e) => s.patch({ step_tool_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="LLM 看门狗（秒）" aside={<SettingStatus s={s} field="step_llm_timeout_seconds" />}>
            <input
              type="number"
              min={1}
              className={NUMBER_INPUT_CLASS}
              value={d.step_llm_timeout_seconds}
              onChange={(e) => s.patch({ step_llm_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="输出尾部保留（字节）" aside={<SettingStatus s={s} field="run_tail_bytes" />}>
            <input
              type="number"
              min={1}
              className={NUMBER_INPUT_CLASS}
              value={d.run_tail_bytes}
              onChange={(e) => s.patch({ run_tail_bytes: Number(e.target.value) })}
            />
          </Field>
          <Field label="工具输出全局钳制（字节）" aside={<SettingStatus s={s} field="tool_output_tail_bytes" />}>
            <input
              type="number"
              min={1}
              className={NUMBER_INPUT_CLASS}
              value={d.tool_output_tail_bytes}
              onChange={(e) => s.patch({ tool_output_tail_bytes: Number(e.target.value) })}
            />
          </Field>
        </>
      )}
    </SettingsSection>
  )
}
