import { Link } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, HelpCircle, XCircle, type LucideIcon } from 'lucide-react'
import type { HealthState, OverviewHealth } from '@/api/overview'
import { humanBytes, relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Phrases } from './Phrases'

// 状态不只靠颜色：每项都有图标与读屏可读的状态名。
const STATE: Record<HealthState, { icon: LucideIcon; label: string; className: string }> = {
  ok: { icon: CheckCircle2, label: '正常', className: 'text-status-success' },
  warn: { icon: AlertTriangle, label: '有隐患', className: 'text-status-warning' },
  down: { icon: XCircle, label: '不可用', className: 'text-status-error' },
  unknown: { icon: HelpCircle, label: '未知', className: 'text-text-tertiary' },
}

interface Item {
  key: string
  name: string
  /** 这一项的日志（日志页按承载它的进程筛好）；平台不收集日志的数据依赖没有 */
  to?: string
  state: HealthState
  /** 读数：几个带标签的短语，窄处按短语折行 */
  text: string[]
  /** 悬停补充（版本号这类不影响健康判断的信息） */
  title?: string
}

/** 往返延迟：同处内网常在 1 毫秒以内。 */
function latency(ms: number): string {
  return `延迟 ${ms < 1 ? '<1' : Math.round(ms)} ms`
}

function runnerText(h: OverviewHealth['runner']): string[] {
  if (h.state === 'unknown') return ['读取失败']
  if (h.instances.length === 0) return ['没有在线实例']
  const active = h.instances.reduce((n, r) => n + r.active, 0)
  const slots = h.instances.reduce((n, r) => n + r.concurrency, 0)
  const stopping = h.instances.filter((r) => r.status !== 'active').length
  const parts = [active === 0 ? '空闲' : `执行 ${active} 个任务`, `并发上限 ${slots}`]
  if (h.instances.length > 1) parts.push(`${h.instances.length} 个实例`)
  if (stopping > 0) parts.push(`${stopping} 个停机中`)
  return parts
}

function proxyText(h: OverviewHealth['proxy']): string[] {
  if (h.state === 'unknown') return ['读取失败']
  if (h.state === 'down') return [h.last_seen_at ? `离线 · 最后在线 ${relativeTime(h.last_seen_at)}` : '未运行']
  if (h.requests_last_hour === null) return ['在线']
  const parts = [`近 1 小时 ${h.requests_last_hour} 个请求`]
  // 近一小时没有流量时，最近一次请求说明它是一直没人用、还是刚停下来
  if (h.requests_last_hour === 0) parts.push(h.last_request_at ? `最近请求 ${relativeTime(h.last_request_at)}` : '从未收到请求')
  return parts
}

function sandboxText(h: OverviewHealth['sandbox']): string[] {
  if (h.state === 'unknown') return ['读取失败']
  if (!h.reachable) return ['不可达']
  return [`工具 ${h.tools_installed}/${h.tools_total}`, h.browser ? '浏览器可用' : '浏览器不可用']
}

function postgresText(h: OverviewHealth['postgres']): string[] {
  if (h.state === 'unknown') return ['读取失败']
  if (h.state === 'down') return ['连接失败']
  return [latency(h.latency_ms), `连接 ${h.connections}/${h.max_connections}`]
}

function redisText(h: OverviewHealth['redis']): string[] {
  if (h.state === 'unknown') return ['读取失败']
  if (h.state === 'down') return ['连接失败']
  const mem = h.max_memory_bytes > 0 ? `${humanBytes(h.used_memory_bytes)}/${humanBytes(h.max_memory_bytes)}` : humanBytes(h.used_memory_bytes)
  return [latency(h.latency_ms), `内存 ${mem}`]
}

/**
 * 组件健康条：三个工作进程与两个数据依赖在同一条带里、以细线分隔，位置固定（凭位置就能找到某一项）。
 * API 不在其中：能看到这个页面，它就在线。每项的读数是带标签的指标，回答同一个问题：健不健康、
 * 负载多高（执行中的任务 / 并发上限、近期流量、能力是否齐全、往返延迟与连接或内存占用）。
 * 状态由图标 + 读屏状态名表达，异常项的名字随状态着色。三个进程链到自己的日志（排查的第一现场）；
 * logOf 给出承载某个组件的日志进程（没有日志时为空，不给链接）。
 */
export function HealthStrip({ health, logOf }: { health: OverviewHealth; logOf: (component: string) => string | undefined }) {
  const logs = (component: string) => {
    const process = logOf(component)
    return process ? `/logs?process=${process}` : undefined
  }
  const items: Item[] = [
    { key: 'runner', name: 'Runner', to: logs('runner'), state: health.runner.state, text: runnerText(health.runner) },
    { key: 'proxy', name: 'Proxy', to: logs('proxy'), state: health.proxy.state, text: proxyText(health.proxy) },
    { key: 'sandbox', name: 'Sandbox', to: logs('sandbox'), state: health.sandbox.state, text: sandboxText(health.sandbox) },
    {
      key: 'postgres',
      name: 'PostgreSQL',
      state: health.postgres.state,
      text: postgresText(health.postgres),
      title: health.postgres.version ? `PostgreSQL ${health.postgres.version}` : undefined,
    },
    {
      key: 'redis',
      name: 'Redis',
      state: health.redis.state,
      text: redisText(health.redis),
      title: health.redis.version ? `Redis ${health.redis.version}` : undefined,
    },
  ]
  return (
    // gap-px 透出底色即分隔线：任意列数下都是一像素细线，不必按列数增减边框
    <ul
      aria-label="组件健康"
      className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-border bg-border lg:grid-cols-5"
    >
      {items.map((it, i) => {
        const s = STATE[it.state]
        const Icon = s.icon
        const abnormal = it.state === 'warn' || it.state === 'down'
        const body = (
          <>
            <span className={cn('flex items-center gap-1.5 text-xs font-medium', abnormal ? s.className : 'text-text-secondary')}>
              <Icon aria-hidden className={cn('h-3.5 w-3.5 flex-shrink-0', s.className)} />
              {it.name}
              <span className="sr-only">：{s.label}，</span>
            </span>
            <Phrases parts={it.text} className="text-sm tabular-nums text-text" />
          </>
        )
        const cell = 'flex h-full flex-col gap-1 px-4 py-3'
        return (
          // 两列时五项余一：末项占满一行，不留空格
          <li
            key={it.key}
            className={cn('min-w-0 bg-surface', i === items.length - 1 && 'col-span-2 lg:col-span-1')}
            title={it.title}
          >
            {it.to ? (
              <Link to={it.to} className={cn(cell, 'transition-colors hover:bg-surface-2 focus-visible:bg-surface-2')}>
                {body}
              </Link>
            ) : (
              <div className={cell}>{body}</div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
