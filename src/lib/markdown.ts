// markdown 渲染：marked 解析 + DOMPurify 消毒，防 XSS（agent 输出当 markdown 富文本渲染）。
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ breaks: true, gfm: true })

// renderMarkdown 把 markdown 文本转成安全 HTML 字符串（供 v-html）。
export function renderMarkdown(src: string): string {
  if (!src) return ''
  const raw = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(raw)
}
