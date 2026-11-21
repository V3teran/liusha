import { cn } from '@/lib/utils'

// 表单控件统一规格（单一事实源）：输入框 / 下拉 / 日期 / 文本域共用同一「框体」，
// 只在尺寸档位上区分。此前全站有 5 套各自手写的控件类（圆角 md/lg 混用、聚焦态
// 一半用辉光一半用 ring、占位符二级/三级灰混用），同屏并排时边缘与焦点表现不一。
//
// 尺寸两档（对齐业界 32/36px 密度档）：
//   md  h-9 · 圆角 lg · text-sm —— 页面筛选栏、表单字段（默认）
//   sm  h-8 · 圆角 md · text-xs —— 密集上下文：分页条、表格行内编辑、画布工具条

export type ControlSize = 'sm' | 'md'

// 窄屏（< sm）字号 ≥16px：iOS Safari 聚焦字号小于 16px 的输入控件时会自动放大页面，
// 用户得手动缩回去。桌面保持密度档的 14 / 12px。
//
// 无效态：取值违反控件自身约束（min / max / step / required，用户动过之后才判定）或调用方标了
// aria-invalid 时边框转为错误色——约束由字段自己表达，不在标签旁常驻「须 >0」之类的说明。
const FRAME =
  'border border-border bg-surface text-text outline-none transition-[border-color,box-shadow] duration-150 ' +
  'placeholder:text-text-tertiary hover:border-border-strong focus:border-accent focus:glow-accent ' +
  'user-invalid:border-status-error aria-[invalid=true]:border-status-error ' +
  'disabled:cursor-not-allowed disabled:opacity-60 max-sm:text-base'

const SIZE: Record<ControlSize, string> = {
  sm: 'h-8 rounded-md px-2.5 text-xs',
  md: 'h-9 rounded-lg px-3 text-sm',
}

/** 单行控件（input / select）类名。宽度由调用方追加。 */
export function controlClass(size: ControlSize = 'md', extra?: string): string {
  return cn(FRAME, SIZE[size], extra)
}

/** 表单字段输入框（md 档、撑满字段宽度）：弹窗/抽屉/配置页表单共用。 */
export const INPUT_CLASS = controlClass('md', 'w-full')

/** 数值输入框（md 档）：数量值不需要整行宽——窄屏撑满，sm 起固定 12rem，右侧留白让标签与说明成为视觉主体。 */
export const NUMBER_INPUT_CLASS = controlClass('md', 'w-full tabular-nums sm:w-48')

/** 表单字段标签：控件上方的小号说明文字（配置抽屉 Field 与各弹窗表单共用）。 */
export const FIELD_LABEL_CLASS = 'text-xs font-medium text-text-secondary'

/** 多行文本域类名（高度由内容/调用方决定）。 */
export const TEXTAREA_CLASS = cn(FRAME, 'w-full rounded-lg px-3 py-2 text-sm leading-relaxed')
