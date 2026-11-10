import { OverrideStatus } from '@/features/config/OverrideStatus'
import type { SettingSection } from './useSettingSection'

// SettingStatus 是设置字段的自定义状态条（未自定义时不渲染）。
export function SettingStatus<T>({ s, field }: { s: SettingSection<T>; field: keyof T & string }) {
  const d = s.detail
  if (!d) return null
  return (
    <OverrideStatus
      state={d.overrides[field]}
      current={d.values[field]}
      busy={s.resetting === field}
      onReset={() => {
        if (window.confirm('把该项恢复为出厂默认？')) void s.resetField(field)
      }}
    />
  )
}

// sectionProps 是分区卡的通用属性（加载 / 保存 / 放弃 / 全部恢复出厂）。
export function sectionProps<T>(s: SettingSection<T>) {
  return {
    loading: s.loading,
    error: s.error,
    saving: s.saving,
    saved: s.saved,
    isDirty: s.isDirty,
    onSave: () => void s.onSave(),
    onDiscard: s.discard,
    customizedCount: s.detail ? Object.keys(s.detail.overrides).length : 0,
    resettingAll: s.resetting === 'all',
    onResetAll: () => {
      if (window.confirm('把本组全部自定义恢复为出厂默认？')) void s.resetField()
    },
  }
}
