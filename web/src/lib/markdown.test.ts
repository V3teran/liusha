import { describe, it, expect } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('空输入返回空串', () => {
    expect(renderMarkdown('')).toBe('')
  })

  it('普通文本渲染成段落', () => {
    expect(renderMarkdown('hello world')).toContain('<p>hello world</p>')
  })

  it('代码块外包 .code-block 容器 + 注入复制按钮', () => {
    const html = renderMarkdown('```bash\nnmap -sV target\n```')
    expect(html).toContain('code-block')
    expect(html).toContain('code-copy')
    // 复制按钮把源码存 data-code
    expect(html).toContain('data-code')
    expect(html).toContain('nmap -sV target')
  })

  it('bash 代码块带 hljs 高亮 class', () => {
    const html = renderMarkdown('```bash\ncurl http://x\n```')
    expect(html).toContain('hljs')
  })

  it('消毒 XSS（script 标签被移除）', () => {
    const html = renderMarkdown('<script>alert(1)</script>正常文本')
    expect(html).not.toContain('<script>')
    expect(html).toContain('正常文本')
  })

  it('行内代码不被包 code-block（只包围栏代码块）', () => {
    const html = renderMarkdown('这是 `inline` 代码')
    expect(html).not.toContain('code-block')
    expect(html).toContain('<code>inline</code>')
  })

  it('裸 URL 后紧跟中文时，链接在 URL 处截断（不吞中文）', () => {
    const html = renderMarkdown('对http://111.229.193.40:34280进行全面侦察，产出清单。')
    // 链接 href 只到端口，中文不并入
    expect(html).toContain('href="http://111.229.193.40:34280"')
    expect(html).not.toContain('34280进行')
    // 中文以普通文本保留
    expect(html).toContain('进行全面侦察')
  })

  it('裸 URL 尾部 ASCII 标点不并入链接', () => {
    const html = renderMarkdown('见 http://example.com/path.')
    expect(html).toContain('href="http://example.com/path"')
    expect(html).not.toContain('path."')
  })

  it('带查询串的裸 URL 完整保留', () => {
    const html = renderMarkdown('访问 http://a.com/x?y=1&z=2 看看')
    expect(html).toContain('href="http://a.com/x?y=1&amp;z=2"')
  })

  it('显式 markdown 链接语法不受影响', () => {
    const html = renderMarkdown('[目标](http://t.com/login.php) 页面')
    expect(html).toContain('href="http://t.com/login.php"')
    expect(html).toContain('>目标</a>')
  })
})
