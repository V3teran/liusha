/**
 * 系统配置 API 客户端（compaction / runtime / proxy_filter 三组业务旋钮）
 *
 * 复用 client.ts 的 get/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 各组全量读写：GET 拉当前快照，PUT 覆写整组。
 *
 * 事实源是 DB；写经后端 settingstore 失效广播——compaction/runtime 令 runner 进程下次现读即生效，
 * proxy_filter 触发 proxy 进程热换流量过滤链（真热改，无需重启）。
 */

import { get, put } from './client'
import type {
  CompactionSettings,
  RuntimeSettings,
  ProxyFilterSettings,
} from './types'

// ── compaction（会话历史压缩）────────────────────────────────────────

export async function getCompactionSettings(): Promise<CompactionSettings> {
  return (await get<{ compaction: CompactionSettings }>('/settings/compaction')).compaction
}

export async function saveCompactionSettings(v: CompactionSettings): Promise<CompactionSettings> {
  return (await put<{ compaction: CompactionSettings }>('/settings/compaction', v)).compaction
}

// ── runtime（工具运行时）─────────────────────────────────────────────

export async function getRuntimeSettings(): Promise<RuntimeSettings> {
  return (await get<{ runtime: RuntimeSettings }>('/settings/runtime')).runtime
}

export async function saveRuntimeSettings(v: RuntimeSettings): Promise<RuntimeSettings> {
  return (await put<{ runtime: RuntimeSettings }>('/settings/runtime', v)).runtime
}

// ── proxy_filter（代理流量过滤规则）──────────────────────────────────

export async function getProxyFilterSettings(): Promise<ProxyFilterSettings> {
  return (await get<{ proxy_filter: ProxyFilterSettings }>('/settings/proxy-filter'))
    .proxy_filter
}

export async function saveProxyFilterSettings(
  v: ProxyFilterSettings,
): Promise<ProxyFilterSettings> {
  return (
    await put<{ proxy_filter: ProxyFilterSettings }>('/settings/proxy-filter', v)
  ).proxy_filter
}
