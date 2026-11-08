import type { ComponentType } from 'react'
import {
  Activity,
  BookOpen,
  Bot,
  Boxes,
  Bug,
  Cpu,
  FolderOpen,
  GitBranch,
  KeyRound,
  ListChecks,
  MessageSquare,
  ScrollText,
  SlidersHorizontal,
  Wrench,
} from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
  /** 高亮判定前缀（子路由聚合），缺省用 to */
  match?: string
}

export interface NavGroup {
  label: string
  items: NavItem[]
}

// 侧栏信息架构：按「做什么」分三组，而非 14 项平铺——
//   作业：一次渗透的上下游（批次/任务 → 对话 → 产出的漏洞、流量、探索图、工件、凭证）
//   观测：平台自身的运行面（LLM 调用审计、进程日志）
//   配置：智能体与其能力来源（工具、技能、模型路由）+ 系统参数
export const NAV_GROUPS: NavGroup[] = [
  {
    label: '作业',
    items: [
      // 任务是对话的上游容器（批次 → 任务 → 对话），置于对话之上。
      { to: '/tasks', label: '任务', icon: ListChecks },
      { to: '/conversations/direct', label: '对话', icon: MessageSquare, match: '/conversations' },
      { to: '/findings', label: '漏洞', icon: Bug },
      { to: '/traffic', label: '代理流量', icon: Activity },
      { to: '/exploration-graph', label: '探索图', icon: GitBranch },
      { to: '/workspace', label: '工作空间', icon: FolderOpen },
      { to: '/credentials', label: '凭证库', icon: KeyRound },
    ],
  },
  {
    label: '观测',
    items: [
      { to: '/llm-audit', label: 'LLM 审计', icon: Cpu },
      { to: '/logs', label: '日志', icon: ScrollText },
    ],
  },
  {
    label: '配置',
    items: [
      { to: '/config/agents', label: '智能体', icon: Bot },
      { to: '/config/tools', label: '工具', icon: Wrench },
      { to: '/config/skills', label: '技能', icon: BookOpen },
      { to: '/config/models', label: 'LLM 配置', icon: Boxes },
      { to: '/config/system', label: '系统配置', icon: SlidersHorizontal },
    ],
  },
]
