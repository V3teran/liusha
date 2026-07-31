import { Markdown } from './Markdown'

interface AssistantTextProps {
  content: string
}

// 助手文字：LLM 叙述/总结/答复，按 markdown 富文本渲染（与推理卡同源消毒）。
// 轻质卡片（细边框+浅底+左上直角），与用户实色气泡（右上直角）形成明确的材质差异——
// 一眼分清"我说的"与"AI 说的"，不再靠 flex-row-reverse 单独撑区分度。
export function AssistantText({ content }: AssistantTextProps) {
  return (
    <Markdown
      content={content}
      className="max-w-[78%] self-start rounded-2xl rounded-tl-md border border-border bg-surface px-4 py-2.5 shadow-sm"
    />
  )
}
