// markdown 渲染：marked 解析 + highlight.js 语法高亮 + DOMPurify 消毒（防 XSS）。
// 按需注册渗透场景常见语言（bash/http/sql/json/xml/python/php），不引全量 190 种，控体积。
// 高亮 token 用 style.css 里的 .hljs-* 规则上色（跟随 CSS 变量 + 深浅主题），不引第三方主题 css。
import { marked } from 'marked'
import { markedHighlight } from 'marked-highlight'
import DOMPurify from 'dompurify'
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
const PURIFY_CONFIG: DOMPurify.Config = {
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

marked.setOptions({ breaks: true, gfm: true })

// renderMarkdown 把 markdown 文本转成安全 HTML 字符串（供 v-html）。
// 渲染后给每个代码块外包一层带「复制」按钮的容器（源码存 data-code，供 ChatThread 事件委托读取）。
export function renderMarkdown(src: string): string {
  if (!src) return ''
  const raw = marked.parse(src, { async: false }) as string
  const clean = DOMPurify.sanitize(raw, PURIFY_CONFIG)
  return wrapCodeBlocks(clean)
}

// wrapCodeBlocks 用 DOMParser 把 <pre><code> 包进 .code-block 容器 + 注入复制按钮。
// 按钮不带内联 handler（CSP 友好），复制逻辑由 markdown 容器的事件委托处理（见 useCodeCopy）。
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
    btn.textContent = '复制'

    pre.replaceWith(wrapper)
    wrapper.appendChild(pre)
    wrapper.appendChild(btn)
  }
  return doc.body.innerHTML
}
