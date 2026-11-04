import { create } from 'zustand'
import * as authApi from '@/api/auth'

/**
 * 登录态全局存储。
 *
 * 三态：checking（应用启动/首帧探测中，防内容闪现）→ authenticated（带账号名，
 * 侧栏头像菜单用）/ anonymous（未登录，路由守卫跳 /login）。
 *
 * 探测只做一次（bootstrap）；后续登录/登出/改密由对应页面调 setAccount 更新。
 * 会话本身在 HttpOnly Cookie 里，前端不经手 token。
 */

type AuthStatus = 'checking' | 'authenticated' | 'anonymous'

interface AuthState {
  status: AuthStatus
  username: string
  /** 探测当前会话（GET /auth/me）。401 由 client 层拦截跳登录，这里兜底置匿名。 */
  bootstrap(): Promise<void>
  /** 登录/初始化成功后写入账号态。 */
  setAccount(username: string): void
  /** 登出/会话被吊销后清为匿名。 */
  clear(): void
}

export const useAuthStore = create<AuthState>((set) => ({
  status: 'checking',
  username: '',
  async bootstrap() {
    try {
      const me = await authApi.me()
      set({ status: 'authenticated', username: me.username })
    } catch {
      // me 401 时 client 层已试图跳登录页；此处兜底置匿名（守卫渲染登录页而非白屏）。
      set({ status: 'anonymous', username: '' })
    }
  },
  setAccount(username) {
    set({ status: 'authenticated', username })
  },
  clear() {
    set({ status: 'anonymous', username: '' })
  },
}))
