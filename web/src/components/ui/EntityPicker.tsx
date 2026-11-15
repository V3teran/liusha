import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Check, ChevronDown, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useDebouncedSearch } from '@/hooks/useDebouncedSearch'
import { controlClass } from './control'

/** 一个候选：value 是实体 ID，label 是主文案，detail 是区分同名实体的次要信息。 */
export interface PickerOption {
  value: string
  label: string
  detail?: string
}

/** 一次候选查询的结果：hasMore 表示还有未列出的（候选限量，靠关键词缩小）。 */
export interface PickerResult {
  options: PickerOption[]
  hasMore: boolean
}

interface EntityPickerProps {
  /** 选中的实体 ID（空 = 未选择）。 */
  value: string
  /** 选中项的显示名：选中项可能不在当前候选里（如经链接带入），由调用方提供。 */
  valueLabel?: string
  onChange: (value: string) => void
  /** 按关键词取候选（服务端检索、限量）。 */
  fetchOptions: (query: string) => Promise<PickerResult>
  /** 候选查询的缓存键前缀（与关键词拼成完整键）。 */
  queryKey: readonly unknown[]
  /** 未选择时的文案，也是「清除选择」那一项的文案（如「全部任务」）。 */
  emptyLabel: string
  searchPlaceholder: string
  /** 控件的可访问名称（如「按任务筛选」）。 */
  ariaLabel: string
  className?: string
}

/**
 * 可搜索的实体选择框：用于取值会持续增长的实体（任务、对话）。取值少而固定的枚举用 Select；
 * 实体一多，原生下拉既不能检索、又要一次拉全量，这里改为候选由服务端按关键词检索并限量。
 *
 * 收起时与 Select 同一框体；展开为锚定浮层（Radix Popover），内含搜索框与候选列表——
 * WAI-ARIA combobox 模式：焦点留在搜索框，↑/↓ 移动高亮，Enter 选中，Esc 收起。
 */
export function EntityPicker({
  value,
  valueLabel,
  onChange,
  fetchOptions,
  queryKey,
  emptyLabel,
  searchPlaceholder,
  ariaLabel,
  className,
}: EntityPickerProps) {
  const listId = useId()
  const listRef = useRef<HTMLUListElement>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const debounced = useDebouncedSearch(query.trim())
  const [active, setActive] = useState(0)

  const result = useQuery({
    queryKey: [...queryKey, debounced],
    queryFn: () => fetchOptions(debounced),
    enabled: open,
    placeholderData: keepPreviousData,
  })
  const options = result.data?.options ?? []
  // 第 0 项是「清除选择」（有选中值时），之后是候选
  const items: PickerOption[] = value ? [{ value: '', label: emptyLabel }, ...options] : options

  // 候选变化时高亮回到首项
  useEffect(() => setActive(0), [debounced, result.data])
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`)
    el?.scrollIntoView?.({ block: 'nearest' })
  }, [active])

  const select = (next: string) => {
    onChange(next)
    setOpen(false)
    setQuery('')
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (items.length === 0) return
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setActive((i) => (i + 1) % items.length)
        return
      case 'ArrowUp':
        e.preventDefault()
        setActive((i) => (i - 1 + items.length) % items.length)
        return
      case 'Enter':
        e.preventDefault()
        if (items[active]) select(items[active].value)
    }
  }

  const display = value ? (valueLabel ?? '已选择') : emptyLabel
  const status = result.isError
    ? '候选加载失败，请重试'
    : result.isPending
      ? '加载中…'
      : options.length === 0
        ? debounced
          ? '没有匹配项'
          : '暂无可选项'
        : ''

  return (
    <Popover.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setQuery('')
      }}
    >
      <Popover.Trigger asChild>
        <button
          type="button"
          aria-label={`${ariaLabel}：${display}`}
          title={value ? valueLabel : undefined}
          className={controlClass(
            'md',
            cn(
              'inline-flex cursor-pointer items-center gap-2',
              value ? 'border-border-strong text-text' : 'text-text-secondary',
              className,
            ),
          )}
        >
          <span className="min-w-0 truncate">{display}</span>
          <ChevronDown aria-hidden className="ml-auto h-4 w-4 flex-shrink-0 text-text-tertiary" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="start"
          sideOffset={6}
          collisionPadding={16}
          aria-label={ariaLabel}
          className="app-popover flex w-[min(440px,calc(100vw-32px))] flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-lg outline-none"
        >
          <div className="relative border-b border-border-subtle p-2">
            <Search aria-hidden className="pointer-events-none absolute left-5 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <input
              role="combobox"
              aria-label={searchPlaceholder}
              aria-expanded
              aria-controls={listId}
              aria-autocomplete="list"
              aria-activedescendant={items.length > 0 ? `${listId}-${active}` : undefined}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={onKeyDown}
              placeholder={searchPlaceholder}
              spellCheck={false}
              autoComplete="off"
              className={controlClass('md', 'w-full pl-9')}
            />
          </div>
          <ul ref={listRef} id={listId} role="listbox" aria-label={ariaLabel} className="max-h-72 overflow-y-auto py-1">
            {items.map((o, i) => (
              <li
                key={o.value || '__clear__'}
                id={`${listId}-${i}`}
                data-index={i}
                role="option"
                aria-selected={o.value === value}
                // 按下即选中，并阻止搜索框失焦
                onMouseDown={(e) => {
                  e.preventDefault()
                  select(o.value)
                }}
                onMouseEnter={() => setActive(i)}
                className={cn(
                  'flex cursor-pointer items-start gap-2 px-3 py-2 text-sm',
                  i === active ? 'bg-surface-2 text-text' : 'text-text-secondary',
                  o.value === '' && 'border-b border-border-subtle',
                )}
              >
                <Check
                  aria-hidden
                  className={cn('mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-accent', o.value !== value && 'invisible')}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate" title={o.label}>
                    {o.label}
                  </span>
                  {o.detail && <span className="block text-xs text-text-tertiary">{o.detail}</span>}
                </span>
              </li>
            ))}
          </ul>
          {(status || result.data?.hasMore) && (
            <p role="status" className="border-t border-border-subtle px-3 py-2 text-xs text-text-tertiary">
              {status || '仅列出最近的部分，输入关键词缩小范围'}
            </p>
          )}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
