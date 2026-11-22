import { useEffect, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as Tabs from '@radix-ui/react-tabs'
import { Button } from '@/components/ui/Button'
import { FIELD_LABEL_CLASS } from '@/components/ui/control'
import { DialogClose } from '@/components/ui/DialogClose'
import { useReturnFocus } from '@/hooks/useReturnFocus'

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

// 配置管理资源共用的居中编辑抽屉外壳（复用发现页 Radix Dialog 模式）。
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

  const returnFocus = useReturnFocus(open)
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed inset-0 m-auto flex h-[88vh] w-[820px] max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-xl border border-accent/30 bg-surface shadow-[var(--shadow-lg),var(--glow-accent)] focus:outline-none" onCloseAutoFocus={returnFocus}>
          <Tabs.Root value={active} onValueChange={setActive} className="flex min-h-0 flex-1 flex-col">
            <div className="flex items-center justify-between border-b border-border px-5 py-4">
              <Dialog.Title className="truncate text-lg font-semibold text-text">{title}</Dialog.Title>
              <DialogClose />
            </div>

            {/* 底线画在分页条内部（inset 阴影）：选中下划线不必下移 1px 去压住边框，分页条也就不会因
                溢出 1px 冒出滚动条。窄屏分页放不下时横向滑动，不显示滚动条。 */}
            <Tabs.List
              aria-label="表单分页"
              className="scrollbar-none flex flex-shrink-0 gap-1 overflow-x-auto px-5 pt-2 shadow-[inset_0_-1px_0_var(--border)]"
            >
              {tabs.map((t) => (
                <Tabs.Trigger
                  key={t.value}
                  value={t.value}
                  className="flex-shrink-0 whitespace-nowrap border-b-2 border-transparent px-3 py-2 text-sm text-text-secondary outline-none transition-colors hover:text-text focus-visible:text-text data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-text"
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
                  // 抽屉体是唯一的滚动区：分页内容随内容长高（min-h-full 只保证短内容也铺满），长文本与
                  // 列表不再各自滚动；内容超出时抽屉体的下内边距落在最后一个元素之后，不贴底。
                  className="flex min-h-full flex-col gap-4 outline-none data-[state=inactive]:hidden"
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

// 表单字段行：label + 控件竖排，配置各页共用。hint 只放附在标签后的简短数据（字数计数、取值约定），
// 不放说明文字——字段的含义由标签与控件自身表达。
export function Field({
  label,
  hint,
  aside,
  children,
}: {
  label: string
  hint?: string
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
