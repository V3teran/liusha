import { cn } from '@/lib/utils'

export interface DailyBar {
  /** 日期 YYYY-MM-DD（查看者时区） */
  date: string
  value: number
}

interface DailyBarsProps {
  /** 图名（读屏与数据表标题） */
  label: string
  data: DailyBar[]
  /** 数值的完整展示形式（含单位），用于提示框与读屏 */
  format: (v: number) => string
  /** 参考线上的刻度标签（紧凑、不带单位）；缺省同 format */
  axis?: (v: number) => string
  /** 当天的明细行（悬停 / 聚焦的提示框与可访问名共用；缺省只有数值） */
  detail?: (index: number) => string[]
}

/** 「11/8」：横轴与提示框里的日期（年份对近 14 天没有信息量）。 */
function shortDate(date: string): string {
  const [, m, d] = date.split('-')
  return `${Number(m)}/${Number(d)}`
}

/**
 * 按天单序列柱图。细柱（每天一格、柱宽封顶）从基线长起；绘图区顶边即最大值，一条淡参考线
 * 贴着它、线上标刻度。每天的数值在悬停 / 键盘聚焦时的提示框里（同时高亮当天整列），不给柱子
 * 逐个标数；单序列不画图例（区块标题即名字）。另附一份只给读屏的数据表。
 * 提示框贴着当天这一格的左缘或右缘展开（前半段向右、后半段向左），不用 transform 居中——
 * 半像素位移会让提示框里的文字发虚。
 */
export function DailyBars({ label, data, format, axis = format, detail }: DailyBarsProps) {
  const max = Math.max(0, ...data.map((d) => d.value))
  const last = data.length - 1
  const mid = Math.floor(last / 2)
  return (
    <figure className="mt-auto flex flex-col gap-1">
      <div aria-hidden className="flex h-4 justify-end text-2xs tabular-nums text-text-tertiary">
        {max > 0 && axis(max)}
      </div>
      {/* 绘图区：参考线与柱子同在这一个盒子里，参考线的位置即最高柱的柱顶 */}
      <div className="relative h-28 border-b border-border-strong">
        {max > 0 && <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 border-t border-dashed border-border" />}
        <div className="absolute inset-0 flex items-end">
          {data.map((d, i) => {
            const pct = max > 0 ? (d.value / max) * 100 : 0
            const isToday = i === last
            const lines = detail ? detail(i) : [format(d.value)]
            return (
              <div
                key={d.date}
                tabIndex={0}
                role="img"
                aria-label={`${shortDate(d.date)}${isToday ? '（今天）' : ''}：${lines.join('，')}`}
                className="group relative flex h-full flex-1 items-end justify-center rounded-t-sm outline-none transition-colors hover:bg-surface-2 focus-visible:bg-surface-2 focus-visible:ring-2 focus-visible:ring-accent"
              >
                {d.value > 0 && (
                  <div
                    className="w-3/5 max-w-4 rounded-t-[2px] bg-(--cat-sky)"
                    // 非零值至少 2px，避免小值在高值旁边消失成「没有」
                    style={{ height: `max(${pct}%, 2px)` }}
                  />
                )}
                <div
                  role="tooltip"
                  className={cn(
                    'pointer-events-none absolute bottom-full z-10 mb-1.5 hidden w-max max-w-64 rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs shadow-md group-hover:block group-focus-visible:block',
                    i <= mid ? 'left-0' : 'right-0',
                  )}
                >
                  <p className="font-medium text-text">
                    {shortDate(d.date)}
                    {isToday && <span className="ml-1 font-normal text-text-tertiary">今天</span>}
                  </p>
                  {lines.map((line) => (
                    <p key={line} className="mt-0.5 tabular-nums text-text-secondary">
                      {line}
                    </p>
                  ))}
                </div>
              </div>
            )
          })}
        </div>
      </div>
      <div aria-hidden className="flex justify-between pt-0.5 text-2xs tabular-nums text-text-tertiary">
        <span>{data[0] ? shortDate(data[0].date) : ''}</span>
        <span>{data[mid] ? shortDate(data[mid].date) : ''}</span>
        <span>今天</span>
      </div>
      {/* sr-only 放在外层盒子上：表格的最小宽度不随 1px 收缩，直接给 table 会把页面撑出横向滚动 */}
      <div className="sr-only">
        <table>
          <caption>{label}</caption>
          <thead>
            <tr>
              <th scope="col">日期</th>
              <th scope="col">{label}</th>
            </tr>
          </thead>
          <tbody>
            {data.map((d, i) => (
              <tr key={d.date}>
                <td>{d.date}</td>
                <td>{(detail ? detail(i) : [format(d.value)]).join('，')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </figure>
  )
}
