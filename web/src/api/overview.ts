/**
 * 「概览」域 API 客户端：数据快照（GET /overview）与组件健康（GET /overview/health）。
 */

import { get } from './client'

export type SeverityCounts = Record<string, number>

export interface UsageTotals {
  calls: number
  errors: number
  in_tokens: number
  out_tokens: number
  cached_tokens: number
}

export interface OverviewTarget {
  host: string
  tasks: number
  active_tasks: number
  open: SeverityCounts
  findings: number
  last_activity_at: string
}

/** 概览快照。按天序列按查看者时区切分，日期为 YYYY-MM-DD，由旧到新、空日补零。 */
export interface OverviewSnapshot {
  generated_at: string
  window: { days: number; since: string }
  /** 进行中的任务：运行与暂停 */
  tasks: { running: number; paused: number }
  findings: {
    open: SeverityCounts
    daily: { date: string; counts: SeverityCounts }[]
    /** 全部发现按类型（不限时间窗）：按全部数降序；name 是展示名 */
    classes: { class: string; name: string; total: number; open: number }[]
  }
  usage: {
    daily: ({ date: string } & UsageTotals)[]
  }
  targets: OverviewTarget[]
}

/** 组件健康状态：unknown = 查不到（检查超出预算或未装配），不等于坏了。 */
export type HealthState = 'ok' | 'warn' | 'down' | 'unknown'

/** 三个工作进程（runner、proxy、sandbox）与两个数据依赖（PostgreSQL、Redis）的健康。 */
export interface OverviewHealth {
  runner: {
    state: HealthState
    /** 在线实例（队列库按心跳登记）；status 为 active 以外即停机中 */
    instances: { host: string; pid: number; started_at: string; concurrency: number; active: number; status: string }[]
  }
  proxy: {
    state: HealthState
    last_seen_at: string | null
    last_request_at: string | null
    /** 近一小时捕获的请求数；读不到时为 null */
    requests_last_hour: number | null
  }
  sandbox: { state: HealthState; reachable: boolean; tools_installed: number; tools_total: number; browser: boolean }
  /** connections 是数据库当前的客户端连接数（全部客户端），max_connections 是上限 */
  postgres: { state: HealthState; version: string; latency_ms: number; connections: number; max_connections: number }
  /** max_memory_bytes 为 0 即未设上限 */
  redis: { state: HealthState; version: string; latency_ms: number; used_memory_bytes: number; max_memory_bytes: number }
}

/** 查看者所在时区（IANA 名）：「天」的边界按它切分。 */
export function viewerTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

/** days 是趋势时间窗（7 / 14 / 30 天，缺省 14）。 */
export function getOverview(tz: string, days: number): Promise<OverviewSnapshot> {
  return get(`/overview?tz=${encodeURIComponent(tz)}&days=${days}`)
}

export function getOverviewHealth(): Promise<OverviewHealth> {
  return get('/overview/health')
}
