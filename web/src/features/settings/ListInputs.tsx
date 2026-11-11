import { useEffect, useState, type InputHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { codesToText, linesToList, listToLines, textToCodes } from './listText'

// 名单 / 状态码的文本编辑：编辑期间以原始文本为草稿，只把解析结果上报——若每次按键都
// 「文本 → 数组 → 文本」回写，尚未输入下一项的换行、逗号会被立即归一化吞掉（末尾回车
// 再输入，两项被拼成一项）。外部值变化（加载、恢复默认、放弃修改）且与当前文本的解析
// 结果不同时，才按外部值重排文本。

function useRawText<T>(value: T, format: (v: T) => string, parse: (s: string) => T) {
  const [raw, setRaw] = useState(() => format(value))
  useEffect(() => {
    setRaw((r) => (JSON.stringify(parse(r)) === JSON.stringify(value) ? r : format(value)))
  }, [value, format, parse])
  return [raw, setRaw] as const
}

type ListTextareaProps = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'value' | 'onChange'> & {
  value: string[]
  onChange: (v: string[]) => void
}

/** 每行一项的字符串名单。 */
export function ListTextarea({ value, onChange, ...rest }: ListTextareaProps) {
  const [raw, setRaw] = useRawText(value, listToLines, linesToList)
  return (
    <textarea
      {...rest}
      value={raw}
      onChange={(e) => {
        setRaw(e.target.value)
        onChange(linesToList(e.target.value))
      }}
    />
  )
}

type CodesInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> & {
  value: number[]
  onChange: (v: number[]) => void
}

/** 逗号 / 空白分隔的状态码。 */
export function CodesInput({ value, onChange, ...rest }: CodesInputProps) {
  const [raw, setRaw] = useRawText(value, codesToText, textToCodes)
  return (
    <input
      {...rest}
      value={raw}
      onChange={(e) => {
        setRaw(e.target.value)
        onChange(textToCodes(e.target.value))
      }}
    />
  )
}
