/**
 * 登录认证域 API 客户端（/auth 系列）。
 *
 * 会话是 HttpOnly Cookie（liusha_session），前端不持有 token：
 * 登录/建号成功即种下，登出/改密由服务端清除。此处函数只关心
 * 业务结果（用户名 / 初始化状态），Cookie 由浏览器自动管理。
 */

import { get, post } from './client'

/** 当前账号；未登录时请求本身会 401（client 层跳登录页）。 */
export interface Me {
  username: string
}

/** 登录；成功种会话 Cookie。失败抛后端中文错误（限流 429 / 凭据错 401）。 */
export async function login(username: string, password: string): Promise<Me> {
  return post('/auth/login', { username, password })
}

/** 登出：服务端吊销会话并清 Cookie；幂等。 */
export async function logout(): Promise<void> {
  await post('/auth/logout')
}

/** 当前账号（路由守卫数据源）。 */
export async function me(): Promise<Me> {
  return get('/auth/me')
}

/** 修改密码：成功后服务端吊销全部会话（含当前）——调用方应引导回登录页。 */
export async function changePassword(
  currentPassword: string,
  newPassword: string,
): Promise<void> {
  await post('/auth/password', {
    current_password: currentPassword,
    new_password: newPassword,
  })
}
