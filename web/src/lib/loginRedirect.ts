/**
 * 登录后回到原来要去的页面：跳登录页时带上 ?next=<站内路径>，登录成功后 replace 到它。
 *
 * next 只接受站内路径：以单个 / 开头，不能是 //host 或 /\host（浏览器会当成协议相对地址跳到
 * 外站）——否则一条精心构造的登录链接就能把刚登录的用户带去钓鱼页。登录页自身不作为去处。
 */
export const LOGIN_PATH = '/login'

/** 登录页地址：next 为站内路径时带上，首页不带（干净 URL）。 */
export function loginPath(next: string): string {
  const target = safeNextPath(next)
  return target === '/' ? LOGIN_PATH : `${LOGIN_PATH}?next=${encodeURIComponent(target)}`
}

/** 校验 next：合法站内路径原样返回，其余一律回首页。 */
export function safeNextPath(next: string | null | undefined): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\')) return '/'
  if (next === LOGIN_PATH || next.startsWith(`${LOGIN_PATH}?`) || next.startsWith(`${LOGIN_PATH}/`)) return '/'
  return next
}
