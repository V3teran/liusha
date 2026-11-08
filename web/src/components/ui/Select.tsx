import type { SelectHTMLAttributes } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { controlClass, type ControlSize } from './control'

interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'size' | 'className'> {
  /** 尺寸档位（md 默认）。 */
  size?: ControlSize
  /** 布局类（宽度/外边距），作用于外层容器；select 撑满容器。 */
  className?: string
}

/**
 * 下拉选择：原生 select（保留系统级键盘、读屏与移动端选择器）+ 统一框体 +
 * 自绘箭头（appearance-none 去掉各浏览器不一致的原生箭头）。
 */
export function Select({ size = 'md', className, children, ...rest }: SelectProps) {
  return (
    <div className={cn('relative inline-flex', className)}>
      <select
        {...rest}
        className={controlClass(size, cn('w-full cursor-pointer appearance-none', size === 'sm' ? 'pr-7' : 'pr-8'))}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden
        className={cn(
          'pointer-events-none absolute top-1/2 -translate-y-1/2 text-text-tertiary',
          size === 'sm' ? 'right-2 h-3.5 w-3.5' : 'right-2.5 h-4 w-4',
        )}
      />
    </div>
  )
}
