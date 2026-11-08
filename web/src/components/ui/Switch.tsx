import * as SwitchPrimitive from '@radix-ui/react-switch'
import { cn } from '@/lib/utils'

// 布尔开关（Radix Switch）：设置语义控件——「启用 / 支持工具调用」这类声明式配置项。
// 视觉对齐全站输入体系：off 态描边胶囊 + surface-2 底，on 态 accent 实底；焦点 glow 与 INPUT_CLASS 同源。
export function Switch({ className, ...props }: SwitchPrimitive.SwitchProps) {
  return (
    <SwitchPrimitive.Root
      className={cn(
        'inline-flex h-[18px] w-8 flex-shrink-0 cursor-pointer items-center rounded-full border border-border bg-surface-3 transition-colors focus-visible:outline-none focus-visible:shadow-[var(--glow-accent)] data-[state=checked]:border-accent data-[state=checked]:bg-accent disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="block h-3.5 w-3.5 translate-x-[2px] rounded-full bg-text-tertiary transition-transform data-[state=checked]:translate-x-[15px] data-[state=checked]:bg-surface" />
    </SwitchPrimitive.Root>
  )
}
