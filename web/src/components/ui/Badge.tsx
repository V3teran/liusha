import type { ReactNode } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/**
 * Badge - 徽章组件
 */

const badgeVariants = cva(
  'inline-flex w-fit items-center gap-1.5 whitespace-nowrap rounded-full font-semibold leading-none',
  {
    variants: {
      variant: {
        soft: '', // 淡底+同色字，颜色由 style prop 传入
        outline: 'border border-current bg-transparent',
      },
      size: {
        sm: 'px-2 py-0.5 text-xs',
        md: 'px-2.5 py-1 text-xs',
      },
    },
    defaultVariants: {
      variant: 'soft',
      size: 'md',
    },
  },
)

interface BadgeProps extends VariantProps<typeof badgeVariants> {
  color?: string // 主色（CSS 颜色值，如 var(--status-neutral)）：点 + 文字色 + 软底
  dot?: boolean // 是否渲染色点
  pulse?: boolean // 进行中：只让色点呼吸（文字恒定不透明，保对比度）；受减弱动效偏好约束
  className?: string
  children: ReactNode
}

export function Badge({ variant, size, color, dot = true, pulse = false, className, children }: BadgeProps) {
  const style =
    variant === 'outline' || !color
      ? undefined
      : {
          // 软底与 --surface 混成不透明色：文字对比度按「色字 / 自身底」校准，
          // 透明底会叠上容器的底色（错误行红底、选中行灰底）把对比度拉到 AA 以下。
          color,
          background: `color-mix(in srgb, ${color} 16%, var(--surface))`,
          borderColor: `color-mix(in srgb, ${color} 30%, var(--surface))`,
        }

  const dotSize = size === 'sm' ? 'h-1 w-1' : 'h-1.5 w-1.5'

  return (
    <span className={cn(badgeVariants({ variant, size }), className)} style={style}>
      {dot && color && (
        <span
          className={cn('flex-shrink-0 rounded-full', dotSize, pulse && 'motion-safe:animate-pulse')}
          style={{ background: color }}
        />
      )}
      {children}
    </span>
  )
}
