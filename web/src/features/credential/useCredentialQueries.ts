import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteCredentialIdentity, listCredentials, saveCredentialIdentity } from '@/api/credentials'
import type { CredentialEntry, CredentialIdentity } from '@/api/types'

// 凭证库数据层：统一走 React Query。列表在服务端检索、排序、分页（跨 host），
// 这里只把响应行映射成表格行。

const credentialKeys = {
  all: ['credentials'] as const,
  list: (search: string, page: number, size: number) => ['credentials', 'list', search, page, size] as const,
}

/** 列表行：一个登录身份一行（凭证集在值列看）。 */
export interface CredentialRow {
  rowId: string
  host: string
  identity: CredentialIdentity
}

/** 当前页的凭证行与匹配总数；翻页、换关键词时保留上一页，表格不闪空。 */
export function useCredentialList(search: string, page: number, size: number) {
  const query = useQuery({
    queryKey: credentialKeys.list(search, page, size),
    queryFn: () => listCredentials(search, page, size),
    placeholderData: keepPreviousData,
  })
  const rows: CredentialRow[] = (query.data?.items ?? []).map(({ host, ...identity }) => ({
    rowId: `${host}/${identity.username}`,
    host,
    identity,
  }))
  return { query, rows, total: query.data?.total ?? 0 }
}

/** 变更后失效凭证列表的全部页。 */
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

