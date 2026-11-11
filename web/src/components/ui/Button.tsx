import type { ButtonHTMLAttributes } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// 按钮统一规格。视觉层级三档 + 危险：
//   primary   实心强调色 —— 页面/表单/区域的主操作（每个区域至多一个）
//   secondary 描边       —— 并列的次操作（取消、导出、重试）
//   ghost     无框       —— 低频辅助操作（清空、折叠）
//   danger    危险色描边 —— 不可逆操作（删除、停止、丢弃）
// 主操作用实心：强调色本身是克制的蓝灰，淡底写法与描边次按钮、标签几乎无法区分，
// 页面上「先点哪个」失去视觉答案。悬停一律朝「更强调」方向（浅色加深、深色提亮）。
// 尺寸：md 36px / sm 32px 与表单控件同档（同一行内底边对齐）；xs 28px 用于行内、
// 卡片内、确认条里的紧凑操作。
export const buttonVariants = cva(
  'inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap font-semibold transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-40 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        primary:
          'bg-accent text-on-accent shadow-xs hover:bg-accent-hover active:bg-accent-active disabled:hover:bg-accent',
        secondary:
          'border border-border bg-surface text-text-secondary hover:border-border-strong hover:text-text active:bg-surface-2 disabled:hover:border-border disabled:hover:text-text-secondary',
        ghost: 'text-text-secondary hover:bg-surface-2 hover:text-text active:bg-surface-3',
        danger:
          'border border-sev-critical/40 text-sev-critical hover:bg-sev-critical/10 active:bg-sev-critical/15 disabled:hover:bg-transparent',
      },
      size: {
        xs: 'h-7 rounded-md px-2.5 text-xs font-medium [&_svg]:h-3.5 [&_svg]:w-3.5',
        sm: 'h-8 rounded-md px-3 text-xs [&_svg]:h-3.5 [&_svg]:w-3.5',
        md: 'h-9 rounded-lg px-4 text-sm [&_svg]:h-4 [&_svg]:w-4',
      },
    },
    defaultVariants: { variant: 'primary', size: 'md' },
  },
)

export type ButtonVariantProps = VariantProps<typeof buttonVariants>

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & ButtonVariantProps

export function Button({ variant, size, className, type = 'button', ...rest }: ButtonProps) {
  return <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...rest} />
}
