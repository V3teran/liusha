import { useQuery } from '@tanstack/react-query'
import { getScenario, listDeletedScenarios, listScenarios } from '@/api/config'

// 场景的服务端数据。键都在 ['scenarios'] 前缀下：场景页的列表、详情、下发入口的选择框共用一个
// 失效口径——保存、删除、恢复出厂后失效该前缀，选择框随之刷新。

export const SCENARIOS_KEY = ['scenarios'] as const

/** 全部场景（下发入口的场景选择框、定时模板上的场景名）。 */
export function useScenarioOptions() {
  return useQuery({ queryKey: [...SCENARIOS_KEY, 'options'], queryFn: listScenarios })
}

/** 被删除、可恢复的内置场景（场景页「已删除的内置」）。 */
export function useDeletedScenarios() {
  return useQuery({ queryKey: [...SCENARIOS_KEY, 'deleted'], queryFn: listDeletedScenarios })
}

/** 单个场景的详情（场景页抽屉）。code 为空时不请求。 */
export function useScenarioDetail(code: string) {
  return useQuery({
    queryKey: [...SCENARIOS_KEY, 'detail', code],
    queryFn: () => getScenario(code),
    enabled: code !== '',
  })
}
