import { describe, expect, it } from 'vitest'
import { proxyAddress } from './proxyAddress'

describe('proxyAddress', () => {
  it('只有端口时主机取控制台所在主机', () => {
    expect(proxyAddress(':58588', '10.0.0.5')).toBe('10.0.0.5:58588')
    expect(proxyAddress(':58588', 'liusha.example.com')).toBe('liusha.example.com:58588')
  })

  it('IPv6 主机加方括号', () => {
    expect(proxyAddress(':8588', '::1')).toBe('[::1]:8588')
    expect(proxyAddress(':8588', '[::1]')).toBe('[::1]:8588')
  })

  it('完整地址原样使用', () => {
    expect(proxyAddress('proxy.example.com:58588', '10.0.0.5')).toBe('proxy.example.com:58588')
    expect(proxyAddress('127.0.0.1:8588', '10.0.0.5')).toBe('127.0.0.1:8588')
  })
})
