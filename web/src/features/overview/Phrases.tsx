import { cn } from '@/lib/utils'

/**
 * 一组短语：视觉上靠间距分段、窄处按短语整段折行（短语内部不断开，「1.4 MB」不会拆成两行）；
 * 读屏在短语之间有停顿。
 */
export function Phrases({ parts, className }: { parts: string[]; className?: string }) {
  return (
    <span className={cn('flex flex-wrap gap-x-2.5 gap-y-0.5', className)}>
      {parts.map((part, i) => (
        <span key={part} className="whitespace-nowrap">
          {part}
          {i < parts.length - 1 && <span className="sr-only">，</span>}
        </span>
      ))}
    </span>
  )
}
