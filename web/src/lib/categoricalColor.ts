// 分类色：无语义的「身份区分」色板（智能体身份、消息角色、指标类别），与表达好坏/
// 严重度的语义色（--sev-* / --status-*）正交。取值在 design-tokens.css 的 --cat-*，
// 深浅主题各自校准，作文字时对面板与自身淡底均满足 WCAG AA（4.5:1）。
//
// 返回 CSS 值字符串，可直接用于 style 与 color-mix()；淡底用 color-mix 派生——
// var() 引用不能再像十六进制那样拼透明度后缀。

export type CategoricalHue = 'violet' | 'emerald' | 'sky' | 'pink' | 'cyan' | 'orange' | 'lime' | 'neutral'

/** 分类色本色（文字、图标、色点、色条）。 */
export function catColor(hue: CategoricalHue): string {
  return `var(--cat-${hue})`
}

/** 分类色淡底（14%，用于身份徽章/头像底）。 */
export function catSoft(hue: CategoricalHue): string {
  return `color-mix(in srgb, var(--cat-${hue}) 14%, var(--surface))`
}
