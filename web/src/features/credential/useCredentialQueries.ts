import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  deleteCredentialIdentity,
  getCredentialDetail,
  listCredentialHosts,
  saveCredentialIdentity,
} from '@/api/credentials'
import type { CredentialEntry, CredentialIdentity } from '@/api/types'

/** 会话身份判别（与后端命名规则同源）：task:<id>:session / 保留名 session。 */
function isSessionIdentityName(username: string): boolean {
  return username === 'session' || (username.startsWith('task:') && username.endsWith(':session'))
}

// 凭证库数据层：统一走 React Query（对齐流量模块 useTrafficQueries 的模式）。
// 列表视图需要「全部 host 的全部登录身份」：先枚举 host 再并行拉各 host 详情
// （host 数量为个位级，扇出可忽略），前端拍平成行——比后端再加一个聚合
// 端点少一条 API 面。

const credentialKeys = {
  all: ['credentials'] as const,
  hosts: ['credentials', 'hosts'] as const,
  detail: (host: string) => ['credentials', 'detail', host] as const,
}

/** host 汇总列表（枚举，只有计数）。 */
export function useCredentialHosts() {
  return useQuery({
    queryKey: credentialKeys.hosts,
    queryFn: () => listCredentialHosts(),
  })
}

/** 列表行：一个登录身份一行（凭证集在值列与详情面板看）。 */
export interface CredentialRow {
  rowId: string
  isSession?: boolean
  host: string
  identity: CredentialIdentity
}

/**
 * 全量行：枚举 host → 并行拉详情 → 每个登录身份一行。
 * 任一 host 详情失败时保留其余 host（凭证库浏览不该被单 host 脏数据卡死），
 * 失败的 host 经 failedHosts 显式上报——不能把「加载失败」伪装成「没有凭证」。
 */
export function useAllCredentialRows() {
  const hostsQuery = useCredentialHosts()
  const hosts = hostsQuery.data ?? []

  const detailsQuery = useQuery({
    queryKey: ['credentials', 'all-details', ...hosts.map((h) => h.host)],
    queryFn: async (): Promise<{ rows: CredentialRow[]; failedHosts: string[] }> => {
      const settled = await Promise.allSettled(hosts.map((h) => getCredentialDetail(h.host)))
      const failedHosts: string[] = []
      const rows: CredentialRow[] = []
      settled.forEach((r, i) => {
        if (r.status === 'rejected') failedHosts.push(hosts[i].host)
      })
      for (const r of settled) {
        if (r.status !== 'fulfilled') continue
        const d = r.value
        for (const identity of d.identities) {
          // 空壳会话身份不进列表：会话快照的价值在它承载的 cookie；没承载
          // 凭据材料的空壳只是内部管道痕迹，展示出来就是一条废行
          if (isSessionIdentityName(identity.username) && identity.credentials.length === 0) {
            continue
          }
          rows.push({
            rowId: `${d.host}/${identity.username}`,
            host: d.host,
            identity,
            // 会话身份（task:<id>:session / session）是任务期自动快照：
            // 只读降噪展示，不参与行内编辑（编辑会把快照升格为持久身份）
            isSession: isSessionIdentityName(identity.username),
          })
        }
      }
      // 倒序（新→旧）：凭证行无后端统一排序出口（host 枚举 + 逐 host 详情拼装），
      // 组装完成后按身份创建时间排——最新录入/会话身份排在最前，浏览以新为优先。
      rows.sort((a, b) => (b.identity.created_at ?? '').localeCompare(a.identity.created_at ?? ''))
      return { rows, failedHosts }
    },
    enabled: hostsQuery.isSuccess && hosts.length > 0,
    placeholderData: (prev) => prev,
  })

  return {
    rows: detailsQuery.data?.rows ?? [],
    failedHosts: detailsQuery.data?.failedHosts ?? [],
    retryDetails: () => void detailsQuery.refetch(),
    hosts,
    isLoading: hostsQuery.isLoading || (detailsQuery.isPending && hosts.length > 0),
    isError: hostsQuery.isError,
    error: hostsQuery.error,
    isRefetching: hostsQuery.isFetching || detailsQuery.isFetching,
  }
}

/** 变更后失效两级缓存（host 计数 + 详情 + 全量行）。 */
function useInvalidateCredentials() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: ['credentials'] })
  }
}

/** upsert 单个登录身份。 */
export function useSaveCredentialIdentity() {
  const invalidate = useInvalidateCredentials()
  return useMutation({
    mutationFn: ({
      host,
      username,
      role,
      credentials,
    }: { host: string; username: string; role?: string; credentials: CredentialEntry[] }) =>
      saveCredentialIdentity(host, { username, role, credentials }),
    onSuccess: invalidate,
  })
}

/** 删除单个登录身份。 */
export function useDeleteCredentialIdentity() {
  const invalidate = useInvalidateCredentials()
  return useMutation({
    mutationFn: ({ host, username }: { host: string; username: string }) =>
      deleteCredentialIdentity(host, username),
    onSuccess: invalidate,
  })
}

