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
      className="rounded-xl border border-border bg-surface shadow-[var(--glow-soft)]"
    >
      <div className="border-b border-border px-5 py-3.5">
        <h2 className="font-mono text-[13.5px] font-semibold text-text">{title}</h2>
        <p className="mt-0.5 text-[12px] text-muted">{subtitle}</p>
      </div>

      {loading ? (
        <div className="tac-cursor px-5 py-10 text-center font-mono text-[13px] text-muted">
          加载中
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-4 px-5 py-4">{children}</div>
          <div className="flex items-center justify-end gap-3 border-t border-border px-5 py-3">
            {error && <span className="text-[12.5px] text-sev-critical">⚠ {error}</span>}
            {saved && !error && !isDirty && (
              <span className="text-[12.5px] text-accent">已保存并热生效</span>
            )}
            {isDirty && !error && (
              <span className="mr-auto text-[12.5px] text-sev-high">● 有未保存改动</span>
            )}
            {onReset && isDirty && (
              <button
                type="button"
                onClick={onReset}
                disabled={saving}
                className="rounded-lg border border-border px-3.5 py-1.5 text-[13px] text-muted transition-colors hover:border-sev-high/50 hover:text-sev-high disabled:opacity-50"
              >
                放弃
              </button>
            )}
            <button
              type="button"
              onClick={onSave}
              disabled={saving || saveDisabled || !isDirty}
              className="rounded-lg bg-accent px-4 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {saving ? '保存中…' : '保存'}
            </button>
          </div>
        </>
      )}
    </section>
  )
}
