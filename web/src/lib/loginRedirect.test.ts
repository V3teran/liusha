import { describe, expect, it } from 'vitest'
import { loginPath, safeNextPath } from './loginRedirect'

describe('loginRedirect', () => {
  it('站内路径原样保留（含查询串）', () => {
    expect(safeNextPath('/findings?conversation=c-1&selected=f-1')).toBe('/findings?conversation=c-1&selected=f-1')
  })

  it('外站与协议相对地址一律回首页（防开放重定向）', () => {
    for (const bad of ['https://evil.test', '//evil.test/x', '/\\evil.test', 'javascript:alert(1)', 'tasks', '', null, undefined]) {
      expect(safeNextPath(bad)).toBe('/')
    }
  })

  it('登录页自身不作为去处', () => {
    expect(safeNextPath('/login')).toBe('/')
    expect(safeNextPath('/login?next=/tasks')).toBe('/')
  })

  it('登录页地址带上编码后的去处，首页不带', () => {
    expect(loginPath('/tasks?selected=t 1')).toBe('/login?next=%2Ftasks%3Fselected%3Dt%201')
    expect(loginPath('/')).toBe('/login')
    expect(loginPath('//evil.test')).toBe('/login')
  })
})
