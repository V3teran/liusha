import { useEffect, useState } from 'react'
import CodeMirror, { type Extension } from '@uiw/react-codemirror'

// 语言包按扩展名动态加载：编辑器核心本身已随本模块懒加载，语言包再细分一层——
// 打开 .py 不必下载 markdown/javascript 解析器。无匹配返回 null（纯文本）。
async function loadLanguage(ext: string): Promise<Extension | null> {
  switch (ext) {
    case 'py':
      return (await import('@codemirror/lang-python')).python()
    case 'js':
    case 'ts':
    case 'jsx':
    case 'tsx':
      return (await import('@codemirror/lang-javascript')).javascript()
    case 'json':
      return (await import('@codemirror/lang-json')).json()
    case 'md':
      return (await import('@codemirror/lang-markdown')).markdown()
    case 'sh':
    case 'bash': {
      const [{ StreamLanguage }, { shell }] = await Promise.all([
        import('@codemirror/language'),
        import('@codemirror/legacy-modes/mode/shell'),
      ])
      return StreamLanguage.define(shell)
    }
    default:
      return null
  }
}

function extensionOf(path: string): string {
  return path.slice(path.lastIndexOf('.') + 1).toLowerCase()
}

interface FileEditorProps {
  path: string
  value: string
  onChange: (value: string) => void
}

/** 工作区文件编辑器：CodeMirror + 按文件扩展名加载的语法高亮。 */
export function FileEditor({ path, value, onChange }: FileEditorProps) {
  const ext = extensionOf(path)
  // 已加载的语言扩展连同其扩展名一起存：切文件时旧语言不会挂到新文件上。
  const [loaded, setLoaded] = useState<{ ext: string; lang: Extension | null } | null>(null)

  useEffect(() => {
    let alive = true
    loadLanguage(ext)
      .then((lang) => {
        if (alive) setLoaded({ ext, lang })
      })
      .catch(() => {
        // 语言包加载失败（网络/部署切换导致 chunk 失效）只影响高亮，退化为纯文本编辑。
        if (alive) setLoaded({ ext, lang: null })
      })
    return () => {
      alive = false
    }
  }, [ext])

  const lang = loaded?.ext === ext ? loaded.lang : null
  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      extensions={lang ? [lang] : []}
      theme="dark"
      height="384px"
      basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: true }}
    />
  )
}
