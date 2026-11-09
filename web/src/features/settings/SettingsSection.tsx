import type { ReactNode } from 'react'
import { Button } from '@/components/ui/Button'

// SettingsSection 是系统配置页每组设置的分区卡：标题 + 说明 + 表单体 + 底部保存条。
// 每组独立加载/保存/错误态，互不影响（一组保存失败不阻塞其他组）。
// 「放弃」撤销未保存的编辑；「全部恢复出厂」清除本组全部自定义（即时生效）。
export function SettingsSection({
  title,
  subtitle,
  loading,
  error,
  saving,
  saved,
  saveDisabled,
  isDirty = false,
  onSave,
  onDiscard,
  customizedCount = 0,
  onResetAll,
  resettingAll = false,
  children,
}: {
  title: string
  subtitle: string
  loading: boolean
  error: string
  saving: boolean
  saved: boolean
  saveDisabled?: boolean
  isDirty?: boolean // 有未保存改动：驱动「未保存」提示 + 放弃按钮 + 保存按钮启用
  onSave: () => void
  onDiscard?: () => void // 放弃未保存的编辑；未传则不渲染放弃按钮
  customizedCount?: number // 本组已自定义的字段数
  onResetAll?: () => void // 全部恢复出厂；有自定义时渲染
  resettingAll?: boolean
  children: ReactNode
}) {
  return (
    <section
      aria-label={title}
      className="rounded-xl border border-border bg-surface shadow-sm"
    >
      <div className="flex items-start justify-between gap-3 border-b border-border px-5 py-4">
        <div>
          <h2 className="font-mono text-sm font-semibold text-text">{title}</h2>
          <p className="mt-0.5 text-xs text-text-secondary">{subtitle}</p>
        </div>
        {onResetAll && customizedCount > 0 && (
          <button
            type="button"
            disabled={resettingAll || saving}
            onClick={onResetAll}
            className="flex-shrink-0 text-xs text-text-secondary underline-offset-2 hover:text-accent hover:underline disabled:opacity-50"
          >
            已自定义 {customizedCount} 项 · 全部恢复出厂
          </button>
        )}
      </div>

      {loading ? (
        <div className="px-5 py-10 text-center font-mono text-sm text-text-secondary">
          加载中
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-4 px-5 py-4">{children}</div>
          <div className="flex items-center justify-end gap-3 border-t border-border px-5 py-3">
            {error && <span className="text-xs text-status-error">⚠ {error}</span>}
            {saved && !error && !isDirty && (
              <span className="text-xs text-accent">已保存并生效</span>
            )}
            {isDirty && !error && (
              <span className="mr-auto text-xs text-status-warning">● 有未保存改动</span>
            )}
            {onDiscard && isDirty && (
              <Button variant="secondary" onClick={onDiscard} disabled={saving}>
                放弃
              </Button>
            )}
            <Button onClick={onSave} disabled={saving || saveDisabled || !isDirty}>
              {saving ? '保存中…' : '保存'}
            </Button>
          </div>
        </>
      )}
    </section>
  )
}
