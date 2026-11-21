import { useLayoutEffect, useRef, type TextareaHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'
import { TEXTAREA_CLASS } from './control'

// 随内容长高的多行输入：不在自身内部滚动——长文本放在抽屉、页面这类已有滚动区的容器里，
// 再叠一层文本框滚动就是两根滚轮。最小高度由调用方用 min-h-* 给出。
export function AutoTextarea({ className, value, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const ref = useRef<HTMLTextAreaElement>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    el.style.height = 'auto'
    // scrollHeight 不含边框：加回上下边框，内容恰好放下、不出现滚动
    const border = el.offsetHeight - el.clientHeight
    el.style.height = `${el.scrollHeight + border}px`
  }, [value])
  return <textarea ref={ref} value={value} className={cn(TEXTAREA_CLASS, 'resize-none overflow-hidden', className)} {...rest} />
}
