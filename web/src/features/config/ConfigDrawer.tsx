import { useEffect, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as Tabs from '@radix-ui/react-tabs'
import { X } from 'lucide-react'

// 抽屉内的一个分页：value 作 Radix Tabs 的稳定键，label 为顶部按钮文案，content 为该页表单。
export interface DrawerTab {
  value: string
  label: string
  content: ReactNode
}

interface ConfigDrawerProps {
  open: boolean
  title: string // 抽屉标题（如「编辑场景」「新建智能体」）
  tabs: DrawerTab[] // 顶部分页：各页按语义分组，避免一条长表单往下滑
  onOpenChange: (open: boolean) => void
  onSave: () => void
  onDelete?: () => void // 仅编辑态传（新建无可删）
  saving?: boolean
  saveDisabled?: boolean // 表单未过校验时禁用保存（跨分页统一判定）
}

// 配置管理资源共用的居中编辑抽屉外壳（复用漏洞页 Radix Dialog 模式）。
// 外壳职责：遮罩 + 面板 + 头部标题/关闭 + 顶部分页栏 + 可滚动主体 + 底部保存/删除。
// 表单主体按 tab 分组注入；每次打开重置到第一个分页，避免残留上一条记录的分页位置。
export function ConfigDrawer({
  open,
  title,
  tabs,
  onOpenChange,
  onSave,
  onDelete,
  saving = false,
  saveDisabled = false,
}: ConfigDrawerProps) {
  const firstTab = tabs[0]?.value
  const [active, setActive] = useState(firstTab)
  // 抽屉每次打开都回到第一个分页（打开态翻转时重置，切换记录不串页）。
  useEffect(() => {
    if (open) setActive(firstTab)
  }, [open, firstTab])

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="tac-modal-overlay fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 z-50 flex max-h-[88vh] w-[820px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border border-accent/30 bg-surface shadow-[var(--shadow-lg),var(--glow-accent)] focus:outline-none">
          <Tabs.Root value={active} onValueChange={setActive} className="flex min-h-0 flex-1 flex-col">
            <div className="tac-dots relative flex items-center justify-between border-b border-border px-5 py-4">
              <Dialog.Title className="font-mono text-base font-semibold text-text">{title}</Dialog.Title>
              <Dialog.Close className="mr-11 text-muted hover:text-text" aria-label="关闭">
                <X className="h-4 w-4" />
              </Dialog.Close>
            </div>

            <Tabs.List
              aria-label="表单分页"
              className="flex flex-shrink-0 gap-1 border-b border-border px-5 pt-2"
            >
              {tabs.map((t) => (
                <Tabs.Trigger
                  key={t.value}
                  value={t.value}
                  className="relative -mb-px border-b-2 border-transparent px-3 py-2 text-[13px] text-muted outline-none transition-colors hover:text-text focus-visible:text-text data-[state=active]:border-accent data-[state=active]:text-accent"
                >
                  {t.label}
                </Tabs.Trigger>
              ))}
            </Tabs.List>

            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4.5">
              {tabs.map((t) => (
                <Tabs.Content
                  key={t.value}
                  value={t.value}
                  className="flex flex-col gap-4 outline-none data-[state=inactive]:hidden"
                >
                  {t.content}
                </Tabs.Content>
              ))}
            </div>

            <div className="flex items-center gap-2.5 border-t border-border px-5 py-4">
              {onDelete && (
                <button
                  type="button"
                  onClick={onDelete}
                  disabled={saving}
                  className="rounded-lg border border-sev-critical/40 px-4 py-1.5 text-[13px] text-sev-critical transition-colors hover:bg-sev-critical/10 disabled:cursor-default disabled:opacity-40"
                >
                  删除
                </button>
              )}
              <button
                type="button"
                onClick={onSave}
                disabled={saving || saveDisabled}
                className="ml-auto rounded-lg bg-accent px-5 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)] disabled:cursor-default disabled:opacity-40 disabled:hover:shadow-none"
              >
                {saving ? '保存中…' : '保存'}
              </button>
            </div>
          </Tabs.Root>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

// 表单字段行：label + 控件竖排，配置三页共用。
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-muted">
        {label}
        {hint && <span className="ml-2 font-normal text-faint">{hint}</span>}
      </span>
      {children}
    </label>
  )
}

// 统一输入框样式（与漏洞抽屉一致）。聚焦态：emerald 描边 + 深色发散荧光晕（战术终端）。
export const INPUT_CLASS =
  'w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-[13px] text-text outline-none transition-shadow focus:border-accent focus:shadow-[var(--glow-accent)] disabled:cursor-not-allowed disabled:opacity-60'
