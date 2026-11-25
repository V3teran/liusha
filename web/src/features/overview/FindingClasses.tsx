import type { CSSProperties } from 'react'
import { Link } from 'react-router-dom'
import type { OverviewSnapshot } from '@/api/overview'
import { Panel, PanelEmpty, PanelLink } from './Panel'

/** 最多列出的行数；更多的类型合成最后一行「其余 N 类」。 */
const MAX_ROWS = 8

/** 各行依次长出的间隔。 */
const STAGGER_MS = 45

type ClassCount = OverviewSnapshot['findings']['classes'][number]

/**
 * 发现类型分布：按全部发现数排序的横向条形——对比十几个类别的多少，条长是最准的编码，类别名
 * 横排也不会挤。每条同一色相分两段：实色是待处理、浅色是已处置，一眼看出哪类还压着没处理。
 * 每行链到按该类型筛好的发现台账；进入时各条依次从左长出（只动 scale，减少动态效果时不动）。
 */
export function FindingClasses({ classes, className }: { classes: ClassCount[]; className?: string }) {
  const total = classes.reduce((n, c) => n + c.total, 0)
  const overflow = classes.length > MAX_ROWS
  const shown = overflow ? classes.slice(0, MAX_ROWS - 1) : classes
  const rest = classes.slice(shown.length)
  const restRow = rest.length
    ? { total: rest.reduce((n, c) => n + c.total, 0), open: rest.reduce((n, c) => n + c.open, 0), kinds: rest.length }
    : null
  const max = Math.max(1, ...shown.map((c) => c.total), restRow?.total ?? 0)

  return (
    <Panel
      title="发现类型"
      meta={total > 0 ? `${classes.length} 类 · ${total} 条` : undefined}
      className={className}
      action={<PanelLink to="/findings">发现台账</PanelLink>}
    >
      {total === 0 ? (
        <PanelEmpty>坐实的发现会按类型汇总在这里。</PanelEmpty>
      ) : (
        <div className="flex flex-col gap-2">
          <ul className="-my-1 flex flex-col">
            {shown.map((c, i) => (
              <li key={c.class}>
                <Link
                  to={`/findings?class=${encodeURIComponent(c.class)}`}
                  title={`共 ${c.total} 条，待处理 ${c.open} 条`}
                  className="-mx-2 grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)_2.5rem] items-center gap-3 rounded-md px-2 py-1.5 transition-colors hover:bg-surface-2 focus-visible:bg-surface-2"
                >
                  <ClassRow label={c.name} muted={c.class === 'other'} total={c.total} open={c.open} max={max} index={i} />
                </Link>
              </li>
            ))}
            {restRow && (
              <li
                aria-label={`其余 ${restRow.kinds} 类：共 ${restRow.total} 条，待处理 ${restRow.open} 条`}
                title={`共 ${restRow.total} 条，待处理 ${restRow.open} 条`}
                className="-mx-2 grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)_2.5rem] items-center gap-3 px-2 py-1.5"
              >
                <ClassRow label={`其余 ${restRow.kinds} 类`} muted total={restRow.total} open={restRow.open} max={max} index={shown.length} detail={false} />
              </li>
            )}
          </ul>
          <p aria-hidden className="flex gap-4 text-2xs text-text-tertiary">
            <span className="inline-flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-sm bg-(--cat-sky)" />
              待处理
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-sm bg-(--cat-sky)/35" />
              已处置
            </span>
          </p>
        </div>
      )}
    </Panel>
  )
}

/**
 * 一行的三格：类型名 · 两段条 · 总数。
 *
 * 可访问名由内容生成、不另写 aria-label：可见的「类型名 总数」照读，条形对读屏隐藏（它表达的是比例，
 * 没有文字），「共 N 条，待处理 M 条」作为隐藏文本接在可见文字之后。语音控制按屏幕上看到的字说出来
 * 选得中它，读屏又读得到条形表达不了的精确数。
 */
function ClassRow({
  label,
  muted,
  total,
  open,
  max,
  index,
  detail = true,
}: {
  label: string
  muted?: boolean
  total: number
  open: number
  max: number
  index: number
  /** 在内容里带上「共 N 条，待处理 M 条」（可访问名由内容生成的行用）；行自己有 aria-label 时不带 */
  detail?: boolean
}) {
  const grow: CSSProperties = { width: `${(total / max) * 100}%`, animationDelay: `${index * STAGGER_MS}ms` }
  return (
    <>
      <span className={`truncate text-xs ${muted ? 'text-text-tertiary' : 'text-text'}`} title={label}>
        {label}
      </span>
      <span aria-hidden className="flex h-2.5 min-w-0">
        <span
          className="flex h-full min-w-0.5 origin-left gap-0.5 overflow-hidden rounded-r-sm motion-safe:animate-[bar-grow_640ms_cubic-bezier(0.16,1,0.3,1)_both]"
          style={grow}
        >
          {open > 0 && <span className="h-full basis-0 bg-(--cat-sky)" style={{ flexGrow: open }} />}
          {total > open && <span className="h-full basis-0 bg-(--cat-sky)/35" style={{ flexGrow: total - open }} />}
        </span>
      </span>
      <span className="text-right text-sm font-medium tabular-nums text-text">{total}</span>
      {detail && <span className="sr-only">：共 {total} 条，待处理 {open} 条</span>}
    </>
  )
}
