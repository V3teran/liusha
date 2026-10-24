import { describe, expect, it } from 'vitest'
import { canonicalHostPreview } from './canonicalHost'

// 归一预览与后端 CanonicalHost 同规则：scheme/userinfo/路径/端口剥离、
// 小写、IPv6 保留、尾点去除。仅 UX 提示，存储以后端归一为权威。
describe('canonicalHostPreview', () => {
  it('剥 scheme/路径/query', () => {
    expect(canonicalHostPreview('http://a.com/x?q=1')).toBe('a.com')
  })
  it('保留端口（端口参与隔离：不同端口是不同应用）', () => {
    expect(canonicalHostPreview('a.com:8080')).toBe('a.com:8080')
    expect(canonicalHostPreview('a.com')).toBe('a.com')
  })
  it('剥 userinfo', () => {
    expect(canonicalHostPreview('http://user:pw@a.com/')).toBe('a.com')
  })
  it('小写与尾点', () => {
    expect(canonicalHostPreview('A.COM.')).toBe('a.com')
  })
  it('IPv6 字面量保留（含端口）', () => {
    expect(canonicalHostPreview('[::1]:8080')).toBe('::1:8080')
  })
  it('空输入返回空串', () => {
    expect(canonicalHostPreview('  ')).toBe('')
  })
})
