import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listProviders,
  saveProvider,
  deleteProvider,
  listRoutes,
  saveRoleRoute,
  deleteRoleRoute,
  getRetrievalModel,
  saveRetrievalModel,
  clearRetrievalKey,
  diagnoseRetrievalModel,
  diagnoseProvider,
  listProviderModels,
} from '@/api/models'
import { listAgents, patchAgent } from '@/api/config'
import type { ProviderConfig, RetrievalModelDraft } from '@/api/types'

// 模型配置数据层：统一走 React Query（对齐凭证库/流量模块的 useXxxQueries 模式）。
// providers 缓存由「对话模型」「能力路由」两个分区共享，任一写操作 invalidate 后两边同步刷新。

export const modelKeys = {
  providers: ['models', 'providers'] as const,
  routing: ['models', 'routing'] as const,
  agents: ['agents', 'configs'] as const,
  retrieval: ['models', 'retrieval'] as const,
}

/** 检索模型配置（单行；保存/清除后同步失效知识库状态缓存——检索方式随密钥切换）。 */
export function useRetrievalModel() {
  return useQuery({
    queryKey: modelKeys.retrieval,
    queryFn: getRetrievalModel,
  })
}

export function useSaveRetrievalModel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: RetrievalModelDraft) => saveRetrievalModel(p),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['models'] })
      void qc.invalidateQueries({ queryKey: ['corpus', 'status'] })
    },
  })
}

export function useClearRetrievalModel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => clearRetrievalKey(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['models'] })
      void qc.invalidateQueries({ queryKey: ['corpus', 'status'] })
    },
  })
}

/** 检索模型实测（表单当前值，api_key 留空测已存密钥）；结果由调用方就地渲染，不进缓存。 */
export function useDiagnoseRetrievalModel() {
  return useMutation({
    mutationFn: (p: RetrievalModelDraft & { api_key?: string }) => diagnoseRetrievalModel(p),
  })
}

/** 测试连接（表单当前值）；结果由调用方就地渲染并回填表单，不进缓存。 */
export function useDiagnoseProvider() {
  return useMutation({ mutationFn: diagnoseProvider })
}

/** 拉取上游模型列表（表单当前值）；结果只供选择默认模型，不进缓存。 */
export function useListProviderModels() {
  return useMutation({ mutationFn: listProviderModels })
}

/** 全量 provider（含 disabled，路由绑定选择器一次拉全）。 */
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
    queryFn: listRoutes,
  })
}

/** 全量智能体配置（complexity 归档的消费面）。 */
export function useAgents() {
  return useQuery({
    queryKey: modelKeys.agents,
    queryFn: listAgents,
  })
}

/** 写后失效模型配置全部缓存（providers 变更会连带影响路由绑定展示）。 */
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

/**
 * 绑定档位 → provider。乐观更新：onMutate 先写缓存让 select 即时反映，
 * 失败回滚快照（组件无需自管回滚态），成功后 invalidate 拉权威值。
 */
type RouteVars = { role: string; providerCode: string }

export function useSaveRoleRoute() {
  const qc = useQueryClient()
  return useMutation<void, Error, RouteVars, { prev: unknown }>({
    mutationFn: async ({ role, providerCode }: RouteVars) => {
      if (providerCode === '') await deleteRoleRoute(role)
      else await saveRoleRoute(role, providerCode)
    },
    onMutate: async ({ role, providerCode }) => {
      await qc.cancelQueries({ queryKey: modelKeys.routing })
      const prev = qc.getQueryData(modelKeys.routing)
      qc.setQueryData(modelKeys.routing, (old: unknown) => {
        // 结构级乐观写：不动类型定义，按 routing 响应形状就地改 routes 数组。
        const cur = old as { routes?: { role: string; provider_code: string }[] } | undefined
        if (!cur?.routes) return old
        const routes = cur.routes.filter((r) => r.role !== role)
        if (providerCode !== '') routes.push({ role, provider_code: providerCode })
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
 * 智能体移档（自定义 agent 的 complexity，与「智能体」页下拉同源；移回出厂档即不算自定义）。
 * 乐观更新 agents 缓存中该行的 complexity，失败回滚。
 */
type MoveVars = { agentCode: string; complexity: string }

export function useSaveAgentComplexity() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentCode, complexity }: MoveVars) => patchAgent(agentCode, { complexity }),
    onMutate: async ({ agentCode, complexity }) => {
      await qc.cancelQueries({ queryKey: modelKeys.agents })
      const prev = qc.getQueryData(modelKeys.agents)
      qc.setQueryData(modelKeys.agents, (old: unknown) => {
        const cur = old as { code: string; complexity?: string }[] | undefined
        if (!cur) return old
        return cur.map((h) => (h.code === agentCode ? { ...h, complexity } : h))
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
