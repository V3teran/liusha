// 系统配置代理过滤规则里，字符串数组用多行文本框编辑（每行一项），
// 状态码数组用逗号/空白分隔单行编辑。这里是双向转换 + 归一（去空、去重顺序保留）。

/** string[] → 多行文本（每项一行）。 */
export function listToLines(list: string[]): string {
  return list.join('\n')
}

/** 多行文本 → string[]：按行拆，去首尾空白，丢空行。 */
export function linesToList(text: string): string[] {
  return text
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

/** number[] → 逗号分隔单行文本。 */
export function codesToText(codes: number[]): string {
  return codes.join(', ')
}

/** 单行文本 → number[]：按逗号/空白拆，丢非法与非正整数，保留顺序。 */
export function textToCodes(text: string): number[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((s) => Number(s))
    .filter((n) => Number.isInteger(n) && n > 0)
}
