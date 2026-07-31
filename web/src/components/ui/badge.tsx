import type { ReactNode } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// shadcn 风格基础组件：色点 + 文字的胶囊标签，供 severity/status/mode 三处共用替代
// 原来各自手写的 <i>色块+文字> 组合（<i> 语义上是斜体强调，不该拿来当颜色块用）。
const badgeVariants = cva(
  'inline-flex w-fit items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-[10.5px] font-semibold leading-none',
  {
    variants: {
      variant: {
        soft: '', // 淡底+同色字，颜色由 style prop 传入（severity/status 色板不固定，走内联样式）
        outline: 'border border-border bg-transparent text-muted',
      },
    },
    defaultVariants: { variant: 'soft' },
  },
)

interface BadgeProps extends VariantProps<typeof badgeVariants> {
  color?: string // 主色：点 + 文字色。soft variant 下背景取该色 16% 透明度
  dot?: boolean // 是否渲染色点（severity/status 场景=true；纯文字徽章可关闭）
  className?: string
  children: ReactNode
}

export function Badge({ variant, color, dot = true, className, children }: BadgeProps) {
  const style =
    variant === 'outline' || !color
      ? undefined
      : { color, background: `color-mix(in srgb, ${color} 16%, transparent)` }
  return (
    <span className={cn(badgeVariants({ variant }), className)} style={style}>
      {dot && color && <span className="h-1.5 w-1.5 flex-shrink-0 rounded-full" style={{ background: color }} />}
      {children}
    </span>
  )
}
