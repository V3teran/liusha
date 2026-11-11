/**
 * 定时模板管理 API 客户端（/schedules：查看 / 启停 / 删除）。
 * 模板由「新建任务」填写 cron 创建（POST /tasks），这里只管它的后续生命周期。
 */

import { del, get, patch } from './client'
import type { ScheduleRow } from './types'

/** 全部定时模板（含已停用），新建的在前。 */
export async function listSchedules(): Promise<ScheduleRow[]> {
  return (await get<{ items: ScheduleRow[] }>('/schedules')).items
}

/** 启用 / 停用；返回写后的模板（启用时下次触发已按当前时刻重算）。 */
export async function setScheduleEnabled(id: string, enabled: boolean): Promise<ScheduleRow> {
  return (await patch<{ schedule: ScheduleRow }>(`/schedules/${encodeURIComponent(id)}`, { enabled })).schedule
}

/** 删除模板；已触发的任务保留（不再标为定时触发）。 */
export async function deleteSchedule(id: string): Promise<void> {
  await del<{ deleted: boolean }>(`/schedules/${encodeURIComponent(id)}`)
}
