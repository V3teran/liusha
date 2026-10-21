// markdown 渲染：marked 解析 + highlight.js 语法高亮 + DOMPurify 消毒（防 XSS）。
// 按需注册渗透场景常见语言（bash/http/sql/json/xml/python/php），不引全量 190 种，控体积。
// 高亮 token 用 style.css 里的 .hljs-* 规则上色（跟随 CSS 变量 + 深浅主题），不引第三方主题 css。
import { marked } from 'marked'
import { markedHighlight } from 'marked-highlight'
import DOMPurify, { type Config } from 'dompurify'
import hljs from 'highlight.js/lib/core'

// —— 按需注册语言（渗透场景：命令 / 流量 / payload / 响应）——
import bash from 'highlight.js/lib/languages/bash'
import shell from 'highlight.js/lib/languages/shell'
import http from 'highlight.js/lib/languages/http'
import sql from 'highlight.js/lib/languages/sql'
import json from 'highlight.js/lib/languages/json'
import xml from 'highlight.js/lib/languages/xml' // HTML / XML 响应体
import python from 'highlight.js/lib/languages/python'
import php from 'highlight.js/lib/languages/php'
import javascript from 'highlight.js/lib/languages/javascript'
import yaml from 'highlight.js/lib/languages/yaml'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('shell', shell)
hljs.registerLanguage('http', http)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('json', json)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('python', python)
hljs.registerLanguage('php', php)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('yaml', yaml)

// DOMPurify 放行 copy 按钮需要的属性（button/data-*）——高亮后我们把源码存 data-code 供复制。
const PURIFY_CONFIG: Config = {
  ADD_ATTR: ['data-code', 'data-lang', 'aria-label'],
}

marked.use(
  markedHighlight({
    // marked-highlight 用 language-xxx class 传语言；未指定或未注册的走 auto（限已注册语言）。
    langPrefix: 'hljs language-',
    highlight(code, lang) {
      const language = lang && hljs.getLanguage(lang) ? lang : ''
      try {
        return language
          ? hljs.highlight(code, { language }).value
          : hljs.highlightAuto(code).value
      } catch {
        return code // 高亮失败退回原文（已由 marked 转义）
      }
    },
  }),
)

// GFM 自动链接的裸 URL 规则会吞掉 URL 后紧跟的所有非空格字符（含中文与全角标点），
// 于是「http://host:port进行全面侦察，产出清单。」被当成一整条超链接。用一个严格的 inline
// 扩展抢在内置 url tokenizer 之前：URL 只接受合法 ASCII 字符（RFC 3986 子集），遇到中文/
// 全角标点即截断；再按 GFM 惯例剥除尾部 ASCII 标点。剩余文字回落为普通文本。
const strictAutolink = {
  name: 'strictAutolink',
  level: 'inline' as const,
  start(src: string) {
    const i = src.search(/https?:\/\//)
    return i < 0 ? undefined : i
  },
  tokenizer(src: string) {
    const m = /^https?:\/\/[A-Za-z0-9._~:/?#[\]@!$&'()*+,;=%-]*/.exec(src)
    if (!m) return undefined
    let url = m[0]
    // 剥除句尾 ASCII 标点（. , ; : ! ?），它们不该并入链接（GFM 同样这么处理）。
    const trail = /[.,;:!?]+$/.exec(url)
    if (trail) url = url.slice(0, url.length - trail[0].length)
    if (url.length <= 'https://'.length) return undefined // 只剩协议头，非有效 URL
    return {
      type: 'link',
      raw: url,
      href: url,
      text: url,
      tokens: [{ type: 'text', raw: url, text: url }],
    }
  },
}

marked.use({ extensions: [strictAutolink] })
marked.setOptions({ breaks: true, gfm: true })

// renderMarkdown 把 markdown 文本转成安全 HTML 字符串（供 v-html）。
// 渲染后给每个代码块外包一层带「复制」按钮的容器（源码存 data-code，供 Markdown 组件事件委托读取）。
export function renderMarkdown(src: string): string {
  if (!src) return ''
  const raw = marked.parse(src, { async: false }) as string
  const clean = DOMPurify.sanitize(raw, PURIFY_CONFIG) as string
  return wrapCodeBlocks(clean)
}

// 代码块复制按钮的图标（lucide copy/check 的 path）：默认 copy，复制成功由 .copied
// class 切换显示 check。注入路径在 DOMPurify 消毒之后，不经过 React 组件树，故内联 SVG。
const CODE_COPY_ICON =
  '<svg class="icon-copy" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>'
const CODE_CHECK_ICON =
  '<svg class="icon-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>'

// wrapCodeBlocks 用 DOMParser 把 <pre><code> 包进 .code-block 容器 + 注入复制按钮。
// 按钮纯图标（与全局 CopyButton 同一形态，提示走 title/aria-label），悬浮代码块右上角；
// 不带内联 handler（CSP 友好），复制逻辑由 markdown 容器的事件委托处理（见 useCodeCopy）。
function wrapCodeBlocks(html: string): string {
  if (!html.includes('<pre')) return html
  const doc = new DOMParser().parseFromString(html, 'text/html')
  for (const pre of Array.from(doc.querySelectorAll('pre'))) {
    const code = pre.querySelector('code')
    if (!code) continue
    const raw = code.textContent ?? ''
    const wrapper = doc.createElement('div')
    wrapper.className = 'code-block'

    const btn = doc.createElement('button')
    btn.className = 'code-copy'
    btn.type = 'button'
    btn.setAttribute('data-code', raw)
    btn.setAttribute('aria-label', '复制代码')
    btn.setAttribute('title', '复制代码')
    btn.innerHTML = CODE_COPY_ICON + CODE_CHECK_ICON

    pre.replaceWith(wrapper)
    wrapper.appendChild(pre)
    wrapper.appendChild(btn)
  }
  return doc.body.innerHTML
}
