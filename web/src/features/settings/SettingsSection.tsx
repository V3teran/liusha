import type { ReactNode } from 'react'
import { Button } from '@/components/ui/Button'

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
              <Button variant="secondary" onClick={onReset} disabled={saving}>
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
