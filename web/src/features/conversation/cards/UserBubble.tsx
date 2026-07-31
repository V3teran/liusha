interface UserBubbleProps {
  content: string
}

// 用户气泡：中性深色块（border-strong 背景 + 主文本色），不用 --accent 填充。
// --accent 在全站承担的是"可交互/可操作"信号（按钮、链接、进行中状态点）；用它做用户气泡
// 背景，会把"这是谁说的"（身份信号）与"这是能点的"（操作信号）混用。border-strong 本身就是
// 为"要和文字对比清楚"设计的 token，直接复用零新增，对比度天然达标，靠色调深浅+位置+圆角
// 尾角区分身份，不抢主题色。
export function UserBubble({ content }: UserBubbleProps) {
  return (
    <div
      data-card="user"
      className="max-w-[78%] whitespace-pre-wrap break-words rounded-2xl rounded-tr-md bg-border-strong px-4 py-2.5 text-sm leading-relaxed text-text shadow-sm"
    >
      {content}
    </div>
  )
}
