import * as ToastPrimitive from '@radix-ui/react-toast'
import { AlertTriangle, CheckCircle2, X } from 'lucide-react'
import { create } from 'zustand'
import { cn } from '@/lib/utils'

/**
 * 全站操作反馈（toast）：异步操作的结果通知——保存、删除、控制等操作失败（或需要确认已完成）时，
 * 在右下角出现、自动消失，不阻塞页面。用 Radix Toast：读屏按礼貌级播报，悬停 / 聚焦时暂停计时，
 * F8 跳到通知区，可滑动或点 × 关闭。
 *
 * 分工：表单提交、字段校验这类「就在眼前」的失败用 InlineError 贴在触发处；区域加载失败用 ErrorState；
 * toast 只给「触发点不在原位或没有可贴的位置」的操作结果（行内按钮、菜单项、抽屉关闭后的回写）。
 */

type ToastTone = 'error' | 'success'

interface ToastItem {
  id: number
  tone: ToastTone
  message: string
}

interface ToastStore {
  items: ToastItem[]
  push: (tone: ToastTone, message: string) => void
  dismiss: (id: number) => void
}

let nextId = 1

export const useToastStore = create<ToastStore>((set) => ({
  items: [],
  push: (tone, message) => set((s) => ({ items: [...s.items, { id: nextId++, tone, message }] })),
  dismiss: (id) => set((s) => ({ items: s.items.filter((t) => t.id !== id) })),
}))

/** 失败要给用户足够时间读完原因；成功只是确认，短停即可。 */
const DURATION: Record<ToastTone, number> = { error: 8000, success: 3000 }

export const toast = {
  error: (message: string) => useToastStore.getState().push('error', message),
  success: (message: string) => useToastStore.getState().push('success', message),
}

const TONE: Record<ToastTone, { icon: typeof AlertTriangle; className: string }> = {
  error: { icon: AlertTriangle, className: 'text-status-error' },
  success: { icon: CheckCircle2, className: 'text-status-success' },
}

/** 通知区：挂在应用根部一次。 */
export function Toaster() {
  const items = useToastStore((s) => s.items)
  const dismiss = useToastStore((s) => s.dismiss)
  return (
    <ToastPrimitive.Provider swipeDirection="right" label="通知">
      {items.map((t) => {
        const { icon: Icon, className } = TONE[t.tone]
        return (
          <ToastPrimitive.Root
            key={t.id}
            type={t.tone === 'error' ? 'foreground' : 'background'}
            duration={DURATION[t.tone]}
            onOpenChange={(open) => !open && dismiss(t.id)}
            className="app-toast flex w-full items-start gap-2.5 rounded-lg border border-border bg-surface px-3.5 py-3 shadow-lg"
          >
            <Icon aria-hidden className={cn('mt-0.5 h-4 w-4 flex-shrink-0', className)} />
            <ToastPrimitive.Description className="min-w-0 flex-1 break-words text-sm text-text">
              {t.message}
            </ToastPrimitive.Description>
            <ToastPrimitive.Close
              aria-label="关闭通知"
              className="-m-1 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
            >
              <X aria-hidden className="h-3.5 w-3.5" />
            </ToastPrimitive.Close>
          </ToastPrimitive.Root>
        )
      })}
      <ToastPrimitive.Viewport className="app-toast-viewport fixed bottom-4 right-4 flex w-[360px] max-w-[calc(100vw-2rem)] flex-col gap-2 outline-none" />
    </ToastPrimitive.Provider>
  )
}
