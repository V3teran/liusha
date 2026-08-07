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
  onSave,
  children,
}: {
  title: string
  subtitle: string
  loading: boolean
  error: string
  saving: boolean
  saved: boolean
  saveDisabled?: boolean
  onSave: () => void
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
            {saved && !error && <span className="text-[12.5px] text-accent">已保存并热生效</span>}
            <button
              type="button"
              onClick={onSave}
              disabled={saving || saveDisabled}
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
