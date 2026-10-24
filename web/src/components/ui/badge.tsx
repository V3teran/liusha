import type { ReactNode } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/**
 * Badge - 徽章组件
 *
 * 设计改进：
 * 1. 更精致的圆角和间距
 * 2. 更清晰的变体区分（soft/outline/solid）
 * 3. 更流畅的过渡效果
 * 4. 统一的尺寸系统
 */

const badgeVariants = cva(
  'inline-flex w-fit items-center gap-1.5 whitespace-nowrap rounded-full font-semibold leading-none transition-all duration-200',
  {
    variants: {
      variant: {
        soft: '', // 淡底+同色字，颜色由 style prop 传入
        outline: 'border border-current bg-transparent',
        solid: 'border-0', // 实底+白字，颜色由 style prop 传入
      },
      size: {
        sm: 'px-2 py-0.5 text-xs',
        md: 'px-2.5 py-1 text-xs',
        lg: 'px-3 py-1.5 text-sm',
      },
    },
    defaultVariants: {
      variant: 'soft',
      size: 'md',
    },
  },
)

interface BadgeProps extends VariantProps<typeof badgeVariants> {
  color?: string // 主色：点 + 文字色/背景色
  dot?: boolean // 是否渲染色点
  className?: string
  children: ReactNode
}

export function Badge({ variant, size, color, dot = true, className, children }: BadgeProps) {
  const style =
    variant === 'outline' || !color
      ? undefined
      : variant === 'solid'
        ? {
            color: '#ffffff',
            background: color,
            borderColor: color,
          }
        : {
            color,
            background: `color-mix(in srgb, ${color} 16%, transparent)`,
            borderColor: `color-mix(in srgb, ${color} 30%, transparent)`,
          }

  const dotSize = {
    sm: 'h-1 w-1',
    md: 'h-1.5 w-1.5',
    lg: 'h-2 w-2',
  }[size || 'md']

  return (
    <span className={cn(badgeVariants({ variant, size }), className)} style={style}>
      {dot && color && (
        <span
          className={cn('flex-shrink-0 rounded-full', dotSize)}
          style={{ background: variant === 'solid' ? '#ffffff' : color }}
        />
      )}
      {children}
    </span>
  )
}
