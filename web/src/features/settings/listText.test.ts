import { describe, expect, it } from 'vitest'
import { linesToList, listToLines, codesToText, textToCodes } from './listText'

describe('listText 双向转换', () => {
  it('listToLines 每项一行', () => {
    expect(listToLines(['a', 'b'])).toBe('a\nb')
    expect(listToLines([])).toBe('')
  })

  it('linesToList 去空白与空行', () => {
    expect(linesToList('a\n  b  \n\n c\n')).toEqual(['a', 'b', 'c'])
    expect(linesToList('   \n\n')).toEqual([])
  })

  it('codesToText 逗号分隔', () => {
    expect(codesToText([204, 304])).toBe('204, 304')
    expect(codesToText([])).toBe('')
  })

  it('textToCodes 支持逗号/空白混合，丢非正整数与非法值', () => {
    expect(textToCodes('204, 304 500')).toEqual([204, 304, 500])
    expect(textToCodes('204,abc, -1, 0, 302')).toEqual([204, 302])
    expect(textToCodes('  ')).toEqual([])
  })
})
