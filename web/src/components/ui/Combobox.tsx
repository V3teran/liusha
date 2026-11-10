import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { controlClass } from './control'

interface ComboboxProps {
  /** 输入框 id（供外部 <label htmlFor> 关联）。 */
  id?: string
  value: string
  /** 可选项；为空时只是普通输入框（不显示展开按钮）。 */
  options: readonly string[]
  onChange: (value: string) => void
  placeholder?: string
  /** 展开按钮的无障碍名称。 */
  toggleLabel?: string
  'aria-describedby'?: string
}

/** 展开方式：点按钮 / 方向键看全部选项；输入时按输入内容筛选。 */
type ListMode = 'all' | 'filter'

/**
 * 可编辑下拉（WAI-ARIA combobox 模式）：既能自由输入，也能点开看到全部选项。
 * 原生 datalist 会按输入框当前值筛选选项——已有值时只剩它自己，看不到其余选项，
 * 无法改选；这里展开按钮总是列出全部选项，输入时才按输入筛选。
 *
 * 键盘：↓/↑ 展开并移动高亮，Enter 选中，Esc 收起；展开按钮不进 Tab 序（焦点留在输入框）。
 */
export function Combobox({
  id,
  value,
  options,
  onChange,
  placeholder,
  toggleLabel = '展开选项',
  'aria-describedby': describedBy,
}: ComboboxProps) {
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<ListMode>('all')
  const [active, setActive] = useState(-1)

  const query = value.trim().toLowerCase()
  const visible = mode === 'filter' && query ? options.filter((o) => o.toLowerCase().includes(query)) : options
  const expanded = open && visible.length > 0

  // 高亮项保持在可视区内（键盘移动时）
  useEffect(() => {
    if (!expanded || active < 0) return
    const el = listRef.current?.children[active] as HTMLElement | undefined
    el?.scrollIntoView?.({ block: 'nearest' })
  }, [expanded, active])

  const showAll = () => {
    setMode('all')
    setActive(Math.max(0, options.indexOf(value)))
    setOpen(true)
  }
  const select = (option: string) => {
    onChange(option)
    setOpen(false)
    inputRef.current?.focus()
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        if (options.length === 0) return
        e.preventDefault()
        if (!expanded) {
          showAll()
          return
        }
        const step = e.key === 'ArrowDown' ? 1 : -1
        setActive((i) => (i + step + visible.length) % visible.length)
        return
      }
      case 'Enter':
        if (expanded && active >= 0 && active < visible.length) {
          e.preventDefault()
          select(visible[active])
        }
        return
      case 'Escape':
        if (expanded) {
          e.preventDefault()
          e.stopPropagation()
          setOpen(false)
        }
        return
    }
  }

  const hasOptions = options.length > 0
  return (
    <div className="relative">
      <input
        ref={inputRef}
        id={id}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={expanded}
        aria-controls={listId}
        aria-activedescendant={expanded && active >= 0 ? `${listId}-${active}` : undefined}
        aria-describedby={describedBy}
        className={controlClass('md', cn('w-full', hasOptions && 'pr-9'))}
        value={value}
        spellCheck={false}
        autoComplete="off"
        placeholder={placeholder}
        onChange={(e) => {
          onChange(e.target.value)
          setMode('filter')
          setActive(-1)
          setOpen(true)
        }}
        onKeyDown={onKeyDown}
        onBlur={() => setOpen(false)}
      />
      {hasOptions && (
        <button
          type="button"
          tabIndex={-1}
          aria-label={toggleLabel}
          aria-controls={listId}
          aria-expanded={expanded}
          // 按下时阻止默认行为：焦点留在输入框，不触发其 blur 收起
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => {
            if (expanded) setOpen(false)
            else showAll()
            inputRef.current?.focus()
          }}
          className="absolute inset-y-0 right-0 flex w-9 cursor-pointer items-center justify-center rounded-r-lg text-text-tertiary transition-colors hover:text-text"
        >
          <ChevronDown className={cn('h-4 w-4 transition-transform duration-150', expanded && 'rotate-180')} />
        </button>
      )}
      <ul
        ref={listRef}
        id={listId}
        role="listbox"
        hidden={!expanded}
        className="absolute left-0 right-0 top-full z-[var(--z-dropdown)] mt-1 max-h-64 overflow-y-auto rounded-lg border border-border bg-surface py-1 shadow-lg"
      >
        {visible.map((option, i) => (
          <li
            key={option}
            id={`${listId}-${i}`}
            role="option"
            aria-selected={option === value}
            // 按下即选中，并阻止输入框失焦（失焦会先收起列表）
            onMouseDown={(e) => {
              e.preventDefault()
              select(option)
            }}
            onMouseEnter={() => setActive(i)}
            className={cn(
              'flex cursor-pointer items-center gap-2 px-3 py-1.5 font-mono text-[13px] text-text-secondary',
              i === active && 'bg-surface-2 text-text',
            )}
          >
            <Check className={cn('h-3.5 w-3.5 flex-shrink-0 text-accent', option !== value && 'invisible')} />
            <span className="truncate">{option}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
