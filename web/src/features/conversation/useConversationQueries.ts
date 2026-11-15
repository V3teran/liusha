import { useQuery } from '@tanstack/react-query'
import { getConversation } from '@/api/conversations'

export const conversationKeys = {
  detail: (id: string) => ['conversations', 'detail', id] as const,
}

/** 单个对话（标题与所属任务）。id 为空不发请求。 */
export function useConversation(id: string) {
  return useQuery({
    queryKey: conversationKeys.detail(id),
    queryFn: () => getConversation(id),
    enabled: id !== '',
  })
}
