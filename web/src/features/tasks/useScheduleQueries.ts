import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteSchedule, listSchedules, setScheduleEnabled } from '@/api/schedules'

// 定时模板管理的 React Query hooks：列表只在抽屉打开时取；启停 / 删除成功后失效列表
// 重取（服务端是事实源：启用会重算下次触发时间，前端不自行推算）。

/** 定时模板列表的 queryKey（「新建任务」创建定时模板后据此失效）。 */
export const SCHEDULES_KEY = ['schedules'] as const

export function useSchedules(enabled: boolean) {
  return useQuery({ queryKey: SCHEDULES_KEY, queryFn: listSchedules, enabled })
}

export function useToggleSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setScheduleEnabled(id, enabled),
    onSettled: () => qc.invalidateQueries({ queryKey: SCHEDULES_KEY }),
  })
}

export function useDeleteSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteSchedule(id),
    onSettled: () => qc.invalidateQueries({ queryKey: SCHEDULES_KEY }),
  })
}
