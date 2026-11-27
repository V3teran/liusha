import { describe, expect, it } from 'vitest'
import { findingLocation } from './findingLocation'

describe('findingLocation', () => {
  it('「方法 主机路径」；缺哪项省哪项，都没有时为空', () => {
    expect(findingLocation({ host: 'a.example:8080', target: { method: 'GET', path: '/a' } })).toBe('GET a.example:8080/a')
    expect(findingLocation({ host: 'a.example' })).toBe('a.example')
    expect(findingLocation({ host: '', target: { method: 'POST', path: '/b' } })).toBe('POST /b')
    expect(findingLocation({ host: '' })).toBe('')
  })
})
