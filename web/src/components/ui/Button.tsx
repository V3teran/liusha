import type { ButtonHTMLAttributes } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// 按钮统一规格。视觉层级三档 + 危险：
//   primary   强调色淡底 —— 页面/表单的主操作（每个区域至多一个）
//   secondary 描边       —— 并列的次操作（取消、导出）
//   ghost     无框       —— 低频辅助操作（清空、折叠）
//   danger    危险色描边 —— 不可逆操作（删除）
// 悬停一律「加深」：此前一半主按钮悬停反而变浅（accent-softer），强调随交互减弱。
// 尺寸与表单控件同档（md 36px / sm 32px），同一行内按钮与输入框底边对齐。
const buttonVariants = cva(
  'inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap font-semibold transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-40 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        primary:
          'bg-accent-soft text-accent hover:bg-[color-mix(in_srgb,var(--accent)_22%,transparent)] active:bg-[color-mix(in_srgb,var(--accent)_28%,transparent)] disabled:hover:bg-accent-soft',
        secondary:
          'border border-border bg-surface text-text-secondary hover:border-border-strong hover:text-text active:bg-surface-2 disabled:hover:border-border disabled:hover:text-text-secondary',
        ghost: 'text-text-secondary hover:bg-surface-2 hover:text-text active:bg-surface-3',
        danger:
          'border border-sev-critical/40 text-sev-critical hover:bg-sev-critical/10 active:bg-sev-critical/15 disabled:hover:bg-transparent',
      },
      size: {
        sm: 'h-8 rounded-md px-3 text-xs [&_svg]:h-3.5 [&_svg]:w-3.5',
        md: 'h-9 rounded-lg px-4 text-sm [&_svg]:h-4 [&_svg]:w-4',
      },
    },
    defaultVariants: { variant: 'primary', size: 'md' },
  },
)

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & VariantProps<typeof buttonVariants>

export function Button({ variant, size, className, type = 'button', ...rest }: ButtonProps) {
  return <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...rest} />
}
