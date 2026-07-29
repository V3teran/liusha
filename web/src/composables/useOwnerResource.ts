import { ref, watch, type Ref } from 'vue'

export interface OwnerResource<T> {
  owner: Ref<string>
  data: Ref<T | null>
  loading: Ref<boolean>
  error: Ref<string>
  /** 后端 404：该 owner 非 active 模式 / 不存在，无此维度数据（passive owner 常态，非错误）。 */
  notActive: Ref<boolean>
  reload: () => Promise<void>
}

/**
 * owner 维度只读资源的统一加载：选中的 owner 变化即重新拉取，集中管理 loading/error 状态。
 * fetchFn 抛 404（如 passive owner 无 sitemap）映射为 notActive，不当作错误展示。
 */
export function useOwnerResource<T>(fetchFn: (ownerID: string) => Promise<T>): OwnerResource<T> {
  const owner = ref('')
  const data = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const error = ref('')
  const notActive = ref(false)

  async function reload() {
    if (!owner.value) return
    loading.value = true
    error.value = ''
    notActive.value = false
    data.value = null
    try {
      data.value = await fetchFn(owner.value)
    } catch (e) {
      const msg = e instanceof Error ? e.message : '加载失败'
      if (msg.includes('404')) notActive.value = true
      else error.value = msg
    } finally {
      loading.value = false
    }
  }

  watch(owner, reload)
  return { owner, data, loading, error, notActive, reload }
}
