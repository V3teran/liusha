import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listProviders,
  saveProvider,
  deleteProvider,
  restoreProviderEnvKey,
  getRouting,
  saveRoleRoute,
  deleteRoleRoute,
} from '@/api/models'
import { getLLMInvocationStat } from '@/api/client'
import { listAgentConfigs, saveAgentComplexity } from '@/api/config'
import type { ProviderConfig } from '@/api/types'

// 模型模块数据层：统一走 React Query（对齐凭证库/流量模块的 useXxxQueries 模式）。
// providers 缓存在「部署」「能力分档」两个 tab 间共享——切 tab 不重拉，
// 任一写操作 invalidate 后两边同步刷新，替代此前每视图手写 useState + reload。

export const modelKeys = {
  providers: ['models', 'providers'] as const,
  routing: ['models', 'routing'] as const,
  agents: ['agents', 'configs'] as const,
  assignments: ['models', 'assignments'] as const,
  llmStat: (taskId: string | undefined) => ['models', 'llm-stat', taskId] as const,
}

/** 全量 provider 部署（含 disabled，路由绑定选择器一次拉全）。 */
export function useProviders() {
  return useQuery({
    queryKey: modelKeys.providers,
    queryFn: listProviders,
  })
}

/** 路由全景：档位/保留 role → provider key。 */
export function useRouting() {
  return useQuery({
    queryKey: modelKeys.routing,
    queryFn: getRouting,
  })
}

/** 全量智能体配置（complexity 归档的消费面）。 */
export function useAgentConfigs() {
  return useQuery({
    queryKey: modelKeys.agents,
    queryFn: listAgentConfigs,
  })
}

/** LLM 调用统计（支持选定 task_id）。 */
export function useLLMInvocationStat(taskId: string | undefined) {
  return useQuery({
    queryKey: modelKeys.llmStat(taskId),
    queryFn: () => (taskId ? getLLMInvocationStat(taskId) : Promise.resolve(null)),
    enabled: !!taskId,
  })
}

/** assignments 路由表（用于导出配置）。 */
export function useAssignments() {
  return useQuery({
    queryKey: modelKeys.assignments,
    queryFn: getRouting,
    select: (data) => data.routes ?? [],
  })
}

/** 写后失效模型模块全部缓存（providers 变更会连带影响路由绑定展示）。 */
function useInvalidateModels() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: ['models'] })
  }
}

/** upsert provider（新建 POST / 编辑 PUT-by-key）。 */
export function useSaveProvider() {
  const invalidate = useInvalidateModels()
  return useMutation({
    mutationFn: ({ provider, isNew }: { provider: ProviderConfig; isNew: boolean }) =>
      saveProvider(provider, isNew),
    onSuccess: invalidate,
  })
}

/** 删除 provider（被路由 FK 引用时后端 409 抛中文原因）。 */
export function useDeleteProvider() {
  const invalidate = useInvalidateModels()
  return useMutation({
    mutationFn: (key: string) => deleteProvider(key),
    onSuccess: invalidate,
  })
}

/** 恢复使用环境变量：清除 UI 设置的加密密钥，密钥解析回落 env 引用。 */
export function useRestoreProviderEnvKey() {
  const invalidate = useInvalidateModels()
  return useMutation({
    mutationFn: (code: string) => restoreProviderEnvKey(code),
    onSuccess: invalidate,
  })
}

/**
 * 绑定档位 → provider。乐观更新：onMutate 先写缓存让 select 即时反映，
 * 失败回滚快照（组件无需自管回滚态），成功后 invalidate 拉权威值。
 */
type RouteVars = { role: string; providerKey: string }

export function useSaveRoleRoute() {
  const qc = useQueryClient()
  return useMutation<void, Error, RouteVars, { prev: unknown }>({
    mutationFn: async ({ role, providerKey }: RouteVars) => {
      if (providerKey === '') await deleteRoleRoute(role)
      else await saveRoleRoute(role, providerKey)
    },
    onMutate: async ({ role, providerKey }) => {
      await qc.cancelQueries({ queryKey: modelKeys.routing })
      const prev = qc.getQueryData(modelKeys.routing)
      qc.setQueryData(modelKeys.routing, (old: unknown) => {
        // 结构级乐观写：不动类型定义，按 routing 响应形状就地改 routes 数组。
        const cur = old as { routes?: { role: string; provider_code: string }[] } | undefined
        if (!cur?.routes) return old
        const routes = cur.routes.filter((r) => r.role !== role)
        if (providerKey !== '') routes.push({ role, provider_code: providerKey })
        return { ...cur, routes }
      })
      return { prev }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(modelKeys.routing, ctx.prev)
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: modelKeys.routing })
    },
  })
}

/**
 * 智能体移档（写 agent.complexity，与「智能体」页下拉同源）。
 * 乐观更新 agents 缓存中该行的 complexity，失败回滚。
 */
type MoveVars = { agentId: string; complexity: string }

export function useSaveAgentComplexity() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, complexity }: MoveVars) =>
      saveAgentComplexity(agentId, complexity),
    onMutate: async ({ agentId, complexity }) => {
      await qc.cancelQueries({ queryKey: modelKeys.agents })
      const prev = qc.getQueryData(modelKeys.agents)
      qc.setQueryData(modelKeys.agents, (old: unknown) => {
        const cur = old as { id: string; complexity?: string }[] | undefined
        if (!cur) return old
        return cur.map((h) => (h.id === agentId ? { ...h, complexity } : h))
      })
      return { prev }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(modelKeys.agents, ctx.prev)
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: modelKeys.agents })
    },
  })
}
