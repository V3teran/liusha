import { useEffect, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as Tabs from '@radix-ui/react-tabs'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { FIELD_LABEL_CLASS } from '@/components/ui/control'

// 抽屉内的一个分页：value 作 Radix Tabs 的稳定键，label 为顶部按钮文案，content 为该页表单。
export interface DrawerTab {
  value: string
  label: string
  content: ReactNode
}

interface ConfigDrawerProps {
  open: boolean
  title: string // 抽屉标题（如「编辑技能」「编辑智能体」）
  tabs: DrawerTab[] // 顶部分页：各页按语义分组，避免一条长表单往下滑
  onOpenChange: (open: boolean) => void
  onSave: () => void
  onDelete?: () => void // 可删资源（如用户自建技能）传；内置/固定资源不传
  saving?: boolean
  saveDisabled?: boolean // 表单未过校验时禁用保存（跨分页统一判定）
}

// 配置管理资源共用的居中编辑抽屉外壳（复用漏洞页 Radix Dialog 模式）。
// 外壳职责：遮罩 + 面板 + 头部标题/关闭 + 顶部分页栏 + 可滚动主体 + 底部保存。
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
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 flex h-[88vh] w-[820px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border border-accent/30 bg-surface shadow-[var(--shadow-lg),var(--glow-accent)] focus:outline-none">
          <Tabs.Root value={active} onValueChange={setActive} className="flex min-h-0 flex-1 flex-col">
            <div className="flex items-center justify-between border-b border-border px-5 py-4">
              <Dialog.Title className="font-mono text-base font-semibold text-text">{title}</Dialog.Title>
              <Dialog.Close className="text-text-secondary hover:text-text" aria-label="关闭">
                <X className="h-4 w-4" />
              </Dialog.Close>
            </div>

            <Tabs.List
              aria-label="表单分页"
              className="flex flex-shrink-0 gap-1 overflow-x-auto border-b border-border px-5 pt-2"
            >
              {tabs.map((t) => (
                <Tabs.Trigger
                  key={t.value}
                  value={t.value}
                  className="relative -mb-px flex-shrink-0 whitespace-nowrap border-b-2 border-transparent px-3 py-2 text-sm text-text-secondary outline-none transition-colors hover:text-text focus-visible:text-text data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-text"
                >
                  {t.label}
                </Tabs.Trigger>
              ))}
            </Tabs.List>

            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
              {tabs.map((t) => (
                <Tabs.Content
                  key={t.value}
                  value={t.value}
                  // h-full：内容区（如工具多选器、长文本域）可以 flex-1 铺满抽屉剩余高度，
                  // 在自己的面板内滚动——抽屉体不出现第二根滚轮。
                  className="flex h-full min-h-0 flex-col gap-4 outline-none data-[state=inactive]:hidden"
                >
                  {t.content}
                </Tabs.Content>
              ))}
            </div>

            <div className="flex items-center gap-2.5 border-t border-border px-5 py-4">
              {onDelete && (
                <Button variant="danger" onClick={onDelete} disabled={saving}>
                  删除
                </Button>
              )}
              <Button className="ml-auto" onClick={onSave} disabled={saving || saveDisabled}>
                {saving ? '保存中…' : '保存'}
              </Button>
            </div>
          </Tabs.Root>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

// 表单字段行：label + 控件竖排，配置三页共用。hint 可传节点（如内嵌跨页链接）。
export function Field({
  label,
  hint,
  aside,
  children,
}: {
  label: string
  hint?: ReactNode
  // aside 渲染在 <label> 之外（控件下方），用于放可交互的状态条（如「恢复默认」）——
  // 按钮嵌在 label 里会把点击转发给控件，也破坏无障碍语义。
  aside?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="flex flex-col gap-1.5">
        <span className={FIELD_LABEL_CLASS}>
          {label}
          {hint && <span className="ml-2 font-normal text-text-tertiary">{hint}</span>}
        </span>
        {children}
      </label>
      {aside}
    </div>
  )
}
