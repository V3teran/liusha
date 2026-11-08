import { describe, expect, it } from 'vitest'
import { canPretty, contentTypeFromRaw, isJsonContentType, prettyRaw, splitHeadBody } from './rawMessage'

describe('splitHeadBody', () => {
  it('CRLF 分界拆头体', () => {
    const [head, body] = splitHeadBody('GET / HTTP/1.1\r\nHost: x\r\n\r\nBODY')
    expect(head).toBe('GET / HTTP/1.1\r\nHost: x')
    expect(body).toBe('BODY')
  })

  it('纯 LF 分界兼容', () => {
    const [head, body] = splitHeadBody('HTTP/1.1 200 OK\nContent-Type: x\n\n{}')
    expect(head).toBe('HTTP/1.1 200 OK\nContent-Type: x')
    expect(body).toBe('{}')
  })

  it('无分界时 body 为空', () => {
    const [head, body] = splitHeadBody('GET / HTTP/1.1')
    expect(head).toBe('GET / HTTP/1.1')
    expect(body).toBe('')
  })
})

describe('contentTypeFromRaw', () => {
  it('从头段解析 Content-Type（字段名大小写不敏感，保留参数）', () => {
    expect(contentTypeFromRaw('POST /x HTTP/1.1\r\ncontent-type: application/json; charset=utf-8\r\n\r\n{}')).toBe(
      'application/json; charset=utf-8',
    )
    expect(contentTypeFromRaw('HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<html>')).toBe('text/html')
  })

  it('无 Content-Type 头返回空串', () => {
    expect(contentTypeFromRaw('GET / HTTP/1.1\r\nHost: x\r\n\r\n')).toBe('')
    expect(contentTypeFromRaw('')).toBe('')
  })
})

describe('isJsonContentType', () => {
  it('识别 json 家族', () => {
    expect(isJsonContentType('application/json')).toBe(true)
    expect(isJsonContentType('application/vnd.api+json')).toBe(true)
    expect(isJsonContentType('application/json; charset=utf-8')).toBe(true)
    expect(isJsonContentType('text/html')).toBe(false)
    expect(isJsonContentType('')).toBe(false)
  })
})

describe('prettyRaw', () => {
  it('JSON body 缩进美化，头段原样', () => {
    const raw = 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"a":1,"b":[2,3]}'
    const out = prettyRaw(raw, 'application/json')
    expect(out).toContain('HTTP/1.1 200 OK')
    expect(out).toContain('"a": 1')
    expect(out).toContain('  ')
  })

  it('非 JSON content-type 原样返回', () => {
    const raw = 'HTTP/1.1 200 OK\r\n\r\n<html></html>'
    expect(prettyRaw(raw, 'text/html')).toBe(raw)
  })

  it('非法 JSON 保底原文，不破坏', () => {
    const raw = 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{not json'
    expect(prettyRaw(raw, 'application/json')).toBe(raw)
  })

  it('空报文原样', () => {
    expect(prettyRaw('', 'application/json')).toBe('')
  })
})

describe('canPretty', () => {
  it('JSON body 存在才可美化', () => {
    expect(canPretty('HTTP/1.1 200 OK\r\n\r\n{}', 'application/json')).toBe(true)
    expect(canPretty('HTTP/1.1 200 OK\r\n\r\n', 'application/json')).toBe(false)
    expect(canPretty('HTTP/1.1 200 OK\r\n\r\n{}', 'text/html')).toBe(false)
    expect(canPretty('', 'application/json')).toBe(false)
  })
})
