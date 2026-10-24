import type { ReactNode } from 'react'

// SettingsSection 是系统配置页每组旋钮的分区卡：标题 + 说明 + 表单体 + 底部保存条。
// 每组独立加载/保存/错误态，互不影响（一组保存失败不阻塞其他组）。
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
  onReset,
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
  onReset?: () => void // 放弃改动回滚到基线；未传则不渲染放弃按钮
  children: ReactNode
}) {
  return (
    <section
      aria-label={title}
      className="rounded-xl border border-border bg-surface shadow-sm"
    >
      <div className="border-b border-border px-5 py-4">
        <h2 className="font-mono text-sm font-semibold text-text">{title}</h2>
        <p className="mt-0.5 text-xs text-text-secondary">{subtitle}</p>
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
              <span className="text-xs text-accent">已保存并热生效</span>
            )}
            {isDirty && !error && (
              <span className="mr-auto text-xs text-status-warning">● 有未保存改动</span>
            )}
            {onReset && isDirty && (
              <button
                type="button"
                onClick={onReset}
                disabled={saving}
                className="rounded-lg border border-border px-4 py-1.5 text-sm text-text-secondary transition-colors hover:border-status-error/50 hover:text-status-error disabled:opacity-50"
              >
                放弃
              </button>
            )}
            <button
              type="button"
              onClick={onSave}
              disabled={saving || saveDisabled || !isDirty}
              className="rounded-lg bg-accent-soft px-4 py-1.5 text-sm text-accent transition-colors hover:bg-accent/10 hover:text-accent-hover disabled:cursor-not-allowed disabled:opacity-60"
            >
              {saving ? '保存中…' : '保存'}
            </button>
          </div>
        </>
      )}
    </section>
  )
}
