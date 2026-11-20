import { useId } from 'react'
import type { CorpusDraft } from '@/api/corpus'
import { INPUT_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'
import { cn } from '@/lib/utils'

// 知识的标题与正文输入（新建对话框与详情抽屉的编辑态共用）。上限与后端一致：标题 80 字、
// 正文 1000 字（正文上限对齐重排模型单篇文档的有效长度）；接近上限时计数变色。

export const MAX_TITLE = 80
export const MAX_CONTENT = 1000

function Counter({ n, max }: { n: number; max: number }) {
  return (
    <span className={cn('font-mono text-[11px] tabular-nums', n > max ? 'text-sev-critical' : 'text-text-tertiary')}>
      {n}/{max}
    </span>
  )
}

/** 按字符（码点）计数，与后端按 rune 校验一致。 */
export function charCount(s: string): number {
  return [...s.trim()].length
}

interface CorpusTextFieldsProps {
  value: CorpusDraft
  onChange: (next: CorpusDraft) => void
  contentRows?: number
  autoFocus?: boolean
}

export function CorpusTextFields({ value, onChange, contentRows = 10, autoFocus }: CorpusTextFieldsProps) {
  const titleId = useId()
  const contentId = useId()
  const titleLen = charCount(value.title)
  const contentLen = charCount(value.content)
  return (
    <>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-baseline justify-between">
          <label htmlFor={titleId} className="text-xs font-medium text-text-secondary">
            标题
          </label>
          <Counter n={titleLen} max={MAX_TITLE} />
        </div>
        <input
          id={titleId}
          autoFocus={autoFocus}
          className={INPUT_CLASS}
          value={value.title}
          aria-invalid={titleLen > MAX_TITLE}
          onChange={(e) => onChange({ ...value, title: e.target.value })}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-baseline justify-between">
          <label htmlFor={contentId} className="text-xs font-medium text-text-secondary">
            正文
          </label>
          <Counter n={contentLen} max={MAX_CONTENT} />
        </div>
        <textarea
          id={contentId}
          rows={contentRows}
          className={cn(TEXTAREA_CLASS, 'resize-y')}
          value={value.content}
          aria-invalid={contentLen > MAX_CONTENT}
          onChange={(e) => onChange({ ...value, content: e.target.value })}
        />
      </div>
    </>
  )
}

/** 草稿可提交：标题、正文非空且不超上限。 */
export function draftValid(d: CorpusDraft): boolean {
  const t = charCount(d.title)
  const c = charCount(d.content)
  return t > 0 && t <= MAX_TITLE && c > 0 && c <= MAX_CONTENT
}
