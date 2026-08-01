interface AvatarProps {
  who: 'user' | 'agent'
}

// 会话头像：agent=鲨齿剑 logo，user=同款深底圆+剪影。两枚 svg 自带深色圆底，
// 外层只补一层与主题呼应的细描边（ring），不再用红色发光阴影——那与石墨深空+emerald
// 的整体基调冲突，是本组件唯一的视觉调整点，双主题下都保持克制、统一。
export function Avatar({ who }: AvatarProps) {
  return (
    <img
      className="h-[30px] w-[30px] flex-shrink-0 rounded-full shadow-sm ring-1 ring-border"
      src={who === 'user' ? '/avatar-user.svg' : '/logo.svg'}
      alt={who === 'user' ? '我' : 'agent'}
      width={30}
      height={30}
    />
  )
}
