import type { ComponentType } from 'react'
import {
  Activity,
  BookOpen,
  Bot,
  Boxes,
  Bug,
  Compass,
  Cpu,
  FolderOpen,
  GitBranch,
  KeyRound,
  Library,
  ListChecks,
  MessageSquare,
  LayoutGrid,
  ScrollText,
  ShieldCheck,
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

// 侧栏信息架构：按「做什么」分三组，而非 17 项平铺——
//   作业：一次渗透的上下游（批次/任务 → 对话 → 产出的发现、流量、探索图、工件、凭证、知识）
//   观测：平台自身的运行面（服务器日志、审计记录、模型调用记录）
//   配置：智能体与其能力来源（工具、技能、模型）+ 系统参数
//
// 条目名只写对象：组名已经说明的不再重复（配置组的条目不带「配置」后缀，观测组的条目不带「日志」后缀），
// 组名没说明的才用后缀补足信息（凭证库、知识库）。条目的地址一律是资源名的一级路径。
export const NAV_GROUPS: NavGroup[] = [
  {
    label: '作业',
    items: [
      // 概览：组件状态、进行中的工作、待处理与近期产出的汇总，作业的起点，居首。
      { to: '/overview', label: '概览', icon: LayoutGrid },
      // 任务是对话的上游容器（批次 → 任务 → 对话），置于对话之上。
      { to: '/tasks', label: '任务', icon: ListChecks },
      { to: '/conversations/direct', label: '对话', icon: MessageSquare, match: '/conversations' },
      { to: '/findings', label: '发现', icon: Bug },
      { to: '/traffic', label: '代理流量', icon: Activity },
      { to: '/exploration-graph', label: '探索图', icon: GitBranch },
      { to: '/workspace', label: '工作空间', icon: FolderOpen },
      { to: '/credentials', label: '凭证库', icon: KeyRound },
      { to: '/corpus', label: '知识库', icon: Library },
    ],
  },
  {
    label: '观测',
    items: [
      { to: '/logs', label: '服务器', icon: ScrollText },
      { to: '/audit', label: '操作', icon: ShieldCheck },
      { to: '/model-calls', label: '模型调用', icon: Cpu },
    ],
  },
  {
    label: '配置',
    items: [
      { to: '/agents', label: '智能体', icon: Bot },
      { to: '/scenarios', label: '场景', icon: Compass },
      { to: '/tools', label: '工具', icon: Wrench },
      { to: '/skills', label: '技能', icon: BookOpen },
      { to: '/models', label: '模型', icon: Boxes },
      { to: '/settings', label: '系统', icon: SlidersHorizontal },
    ],
  },
]
