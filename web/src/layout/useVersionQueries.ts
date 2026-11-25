import { useQuery } from '@tanstack/react-query'
import { getVersion } from '@/api/version'

export const versionKeys = {
  current: () => ['version'] as const,
}

/** 程序版本：一次页面生命周期内不会变（升级要换程序、页面随之重载），取一次就一直用。 */
export function useVersion() {
  return useQuery({
    queryKey: versionKeys.current(),
    queryFn: getVersion,
    staleTime: Infinity,
    // 取不到就不显示：版本号是点缀，不值得重试，更不该在控制台里刷错误
    retry: false,
  })
}
