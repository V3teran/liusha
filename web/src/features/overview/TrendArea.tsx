import { useId } from 'react'
import { cn } from '@/lib/utils'

/** 顶部呼吸空间（绘图高度的百分比）：顶格线与它的刻度标签不贴图顶。 */
const HEADROOM = 20

export interface TrendPoint {
  /** 日期 YYYY-MM-DD（查看者时区） */
  date: string
  value: number
}

interface TrendAreaProps {
  /** 图名（读屏与数据表标题） */
  label: string
  data: TrendPoint[]
  /** 线与渐变填充的颜色（设计 Token 的 CSS 变量，如 var(--cat-orange)） */
  color: string
  /** 数值的完整展示形式（含单位），用于提示框与读屏 */
  format: (v: number) => string
  /** 网格线上的刻度标签（紧凑、不带单位）；缺省同 format */
  axis?: (v: number) => string
  /** 当天的明细行（悬停 / 聚焦的提示框与可访问名共用；缺省只有数值） */
  detail?: (index: number) => string[]
}

/**
 * 整齐刻度：从 {1,2,3,5} × 10^k 的阶梯里取最小的 s，使 3s ≥ max——三个网格值（3s / 2s / s）
 * 都是整数或紧凑数，不会出现 17.3333 这样的脏标签。
 */
function niceStep(max: number): number {
  const base = Math.pow(10, Math.floor(Math.log10(Math.max(max / 3, Number.EPSILON))))
  for (const m of [1, 2, 3, 5, 10]) {
    const step = m * base
    if (3 * step >= max) return step
  }
  return 10 * base
}

/** 「11/8」：横轴与提示框里的日期（年份对短窗没有信息量）。 */
function shortDate(date: string): string {
  const [, m, d] = date.split('-')
  return `${Number(m)}/${Number(d)}`
}

/**
 * 单调三次插值（Fritsch–Carlson）把折线拟成平滑曲线：切线在相邻数据点之间单调滑过，
 * 不会像普通样条那样在零值密布的序列里上下过冲、画出发明出来的波峰。
 * 返回 SVG 路径（坐标系 0–100，x 由旧到新、y 向下；面积路径沿基线闭合）。
 */
export interface SmoothCurve {
  line: string
  area: string
  /** 每段的 [端点y, 控制点1y, 控制点2y, 端点y]——测试断言凸包不过冲用。 */
  segments: Array<[number, number, number, number]>
}

export function smoothPaths(values: number[], max: number): SmoothCurve {
  const n = values.length
  if (n === 0) return { line: '', area: '', segments: [] }
  // 顶部留 20% 呼吸空间：数据峰值映射到 y=20（顶格线高度），圆滑鼓出的峰顶不会冲出图顶。
  const y = values.map((v) => (max > 0 ? HEADROOM + (1 - v / max) * (100 - HEADROOM) : 100))
  if (n === 1) {
    const line = `M 0 ${y[0]} L 100 ${y[0]}`
    return { line, area: `${line} L 100 100 L 0 100 Z`, segments: [] }
  }
  const dx = 100 / (n - 1)

  // Catmull–Rom → 三次贝塞尔：控制点取相邻点的差分 / 6，曲线圆滑穿过数据点、C1 连续
  // （进出切线由同一组邻居决定，无条件成立，无断折）。尖峰顶端圆滑鼓出、零值处小幅
  // 沉到基线下——前者落在顶部余量内，后者被 SVG 裁切吃掉，均不可见。
  const segments: Array<[number, number, number, number]> = []
  for (let i = 0; i < n - 1; i++) {
    const prev = y[Math.max(0, i - 1)]
    const next = y[Math.min(n - 1, i + 2)]
    segments.push([y[i], y[i] + (y[i + 1] - prev) / 6, y[i + 1] - (next - y[i]) / 6, y[i + 1]])
  }

  let line = `M 0 ${y[0].toFixed(3)}`
  for (let i = 0; i < segments.length; i++) {
    const x0 = i * dx
    const x1 = (i + 1) * dx
    line += ` C ${(x0 + dx / 3).toFixed(3)} ${segments[i][1].toFixed(3)}, ${(x1 - dx / 3).toFixed(3)} ${segments[i][2].toFixed(3)}, ${x1.toFixed(3)} ${segments[i][3].toFixed(3)}`
  }
  return { line, area: `${line} L 100 100 L 0 100 Z`, segments }
}

/**
 * 按天平滑面积图：细线沿单调曲线滑过、线下是同色渐变（往下淡到透明）；虚线网格三档、
 * 左侧刻度。每天的数值在悬停 / 键盘聚焦时的提示框里（当天整列高亮 + 十字标线），不给点
 * 逐个标数；单序列不画图例（区块标题即名字）。另附一份只给读屏的数据表。
 * 提示框贴着当天这一列的左缘或右缘展开（前半段向右、后半段向左），不用 transform 居中——
 * 半像素位移会让提示框里的文字发虚。
 */
export function TrendArea({ label, data, color, format, axis = format, detail }: TrendAreaProps) {
  const gid = useId()
  const rawMax = Math.max(0, ...data.map((d) => d.value))
  const step = niceStep(rawMax)
  const max = step * 3 // 满刻度取整后，数据峰落在顶格线之下（与参考图一致），标签永远干净
  const last = data.length - 1
  const mid = Math.floor(last / 2)
  const grid = rawMax > 0 ? [max, max - step, max - 2 * step] : []
  const { line, area } = smoothPaths(
    data.map((d) => d.value),
    max,
  )

  return (
    <figure className="mt-auto flex flex-col gap-1">
      <div className="relative pl-10">
        {/* 网格线：值越高越靠上（最大值贴顶），刻度标签挂在图左缘 */}
        {grid.map((v) => {
          const top = HEADROOM + (1 - v / max) * (100 - HEADROOM)
          return (
            <div key={v} aria-hidden className="absolute inset-x-0 flex -translate-y-1/2 items-center" style={{ top: `${top}%` }}>
              <span className="w-10 flex-shrink-0 pr-1.5 text-right text-2xs tabular-nums text-text-tertiary">{axis(v)}</span>
              <div className="h-px flex-1 border-t border-dashed border-border" />
            </div>
          )
        })}

        {/* 绘图区：SVG 只画面积与线；文本留给 HTML（preserveAspectRatio=none 会把字拉变形） */}
        <div className="relative h-32 border-b border-border-strong">
          <svg aria-hidden className="absolute inset-0 h-full w-full" viewBox="0 0 100 100" preserveAspectRatio="none">
            <defs>
              <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={color} stopOpacity="0.34" />
                <stop offset="100%" stopColor={color} stopOpacity="0.03" />
              </linearGradient>
            </defs>
            {area && <path d={area} fill={`url(#${gid})`} />}
            {line && <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" vectorEffect="non-scaling-stroke" />}
          </svg>

          {/* 命中列：每天一格，悬停 / 聚焦整列高亮并展开提示框 */}
          <div className="absolute inset-0 flex">
            {data.map((d, i) => {
              const isToday = i === last
              const lines = detail ? detail(i) : [format(d.value)]
              return (
                <div
                  key={d.date}
                  tabIndex={0}
                  role="img"
                  aria-label={`${shortDate(d.date)}${isToday ? '（今天）' : ''}：${lines.join('，')}`}
                  className="group relative h-full flex-1 outline-none transition-colors hover:bg-surface-2 focus-visible:bg-surface-2 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent"
                >
                  {/* 十字标线：贴着当天列的中缝，悬停 / 聚焦时出现 */}
                  <div
                    aria-hidden
                    className="pointer-events-none absolute inset-y-0 left-1/2 hidden border-l border-dashed border-accent/50 group-hover:block group-focus-visible:block"
                  />
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
                    {lines.map((l) => (
                      <p key={l} className="mt-0.5 tabular-nums text-text-secondary">
                        {l}
                      </p>
                    ))}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>
      <div aria-hidden className="flex justify-between pl-10 pt-0.5 text-2xs tabular-nums text-text-tertiary">
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
