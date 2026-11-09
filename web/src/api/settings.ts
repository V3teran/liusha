/**
 * 系统设置 API 客户端（compaction / runtime / proxy_filter 三组，路由段即分组取值）。
 *
 * 生效值 = 出厂默认（代码 + config.yaml 部署值）叠加用户自定义。PATCH 只提交改动的字段
 * （名单字段为 SetChange），DELETE …/overrides[/:field] 恢复出厂。写后后端广播失效：
 * runner 下次现读即新值，proxy 收到 proxy_filter 失效后热换过滤链。
 */

import { get, patch, del } from './client'
import type { RuntimeSettings, SettingsByGroup, SettingsDetail, SettingsGroup, SetChange } from './types'

/** 一组设置的修改：数值字段为新值，名单字段为 SetChange。 */
export type SettingsPatch<T> = {
  [K in keyof T]?: T[K] extends Array<infer E> ? SetChange<E> : T[K]
}

export async function getSettings<G extends SettingsGroup>(group: G): Promise<SettingsDetail<SettingsByGroup[G]>> {
  return get<SettingsDetail<SettingsByGroup[G]>>(`/settings/${group}`)
}

export async function patchSettings<G extends SettingsGroup>(
  group: G,
  changes: SettingsPatch<SettingsByGroup[G]>,
): Promise<SettingsDetail<SettingsByGroup[G]>> {
  return patch<SettingsDetail<SettingsByGroup[G]>>(`/settings/${group}`, changes)
}

/** 把某组设置的某字段（不传则整组）恢复为出厂默认。 */
export async function resetSettings<G extends SettingsGroup>(
  group: G,
  field?: string,
): Promise<SettingsDetail<SettingsByGroup[G]>> {
  const suffix = field ? `/${encodeURIComponent(field)}` : ''
  return del<SettingsDetail<SettingsByGroup[G]>>(`/settings/${group}/overrides${suffix}`)
}

/** 工具运行时设置的生效值（LLM 审计页的超时判定与之同源）。 */
export async function getRuntimeSettings(): Promise<RuntimeSettings> {
  return (await getSettings('runtime')).values
}
