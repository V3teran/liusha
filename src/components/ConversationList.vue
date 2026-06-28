<script setup lang="ts">
// 对话侧栏：挂载时拉对话列表，点击向上抛选中 ID；顶部「+ 新对话」抛 new。
// 列表项展示真实状态点 + 标题 + 相对时间，hover 出 ⋯ 更多菜单（删除，留扩展位），当前选中高亮。
// 对齐 ChatGPT/Claude/Claude Code 侧栏惯例。
import { onMounted, ref, onBeforeUnmount } from 'vue'
import { listConversations, deleteConversation } from '../api/client'
import type { Conversation } from '../api/types'
import { relativeTime, fullTime } from '../lib/format'
import { scanStatusMeta } from '../lib/scanStatus'

const props = defineProps<{ activeId?: string }>()
const items = ref<Conversation[]>([])
const emit = defineEmits<{ select: [convID: string]; new: []; deleted: [convID: string] }>()

async function refresh() {
  items.value = await listConversations()
}
onMounted(refresh)
defineExpose({ refresh })

// 自适应轮询：仅当列表存在「进行中(active)」对话时每 8s 刷新——感知后台对话跑完/状态变化，
// 彻底覆盖「看着对话 A、对话 B 后台跑完」场景。全部终态则不轮询（零浪费）；菜单开着时跳过（不打断操作）。
let pollTimer: number | undefined
onMounted(() => {
  pollTimer = window.setInterval(() => {
    if (menuOpen.value) return // 用户正在操作菜单，别刷新打断
    if (items.value.some((c) => c.RunStatus === 'active')) refresh()
  }, 8000)
})
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

// 真实运行态 → 中文标签 + 色（共享 scanStatusMeta，与顶部状态栏统一一套词）。用 RunStatus
// 派生真实态，不用僵尸 Status。
function statusMeta(c: Conversation) {
  return scanStatusMeta(c.RunStatus)
}

// 标题：去「我要扫描」前缀 + 截取 host 让列表更易读；空则回退短 id（智能标题由后端回填）。
function displayTitle(c: Conversation): string {
  const raw = (c.Title || '').replace(/^我要扫描\s*/, '').trim()
  if (!raw) return c.ID.slice(0, 8)
  const host = raw.match(/https?:\/\/([^/\s]+)/)?.[1]
  return host ? host + raw.replace(/https?:\/\/[^/\s]+/, '').slice(0, 24) : raw.slice(0, 40)
}

// 悬停 tooltip：展示完整标题（仅去「我要扫描」前缀，不截断）。
function fullTitle(c: Conversation): string {
  return (c.Title || '').replace(/^我要扫描\s*/, '').trim() || c.ID
}

// ⋯ 更多菜单：开/关 + 点外部关闭。
// 菜单经 Teleport 渲染到 body、用 fixed 视口坐标定位——逃离对话列表 ul 的 overflow 裁剪
// （否则底部/边缘列表项的下拉菜单会被 ul 的 overflow-y:auto 裁掉，显示不全）。
const menuOpen = ref<string>('')
const menuConv = ref<Conversation | null>(null)
const menuStyle = ref<Record<string, string>>({})
function toggleMenu(c: Conversation, ev: Event) {
  ev.stopPropagation()
  if (menuOpen.value === c.ID) {
    closeMenu()
    return
  }
  const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
  // 右对齐按钮、按钮下方 4px；左缘 clamp 防贴边溢出屏幕。
  menuStyle.value = { top: `${r.bottom + 4}px`, left: `${Math.max(8, r.right - 140)}px` }
  menuConv.value = c
  menuOpen.value = c.ID
}
function closeMenu() {
  menuOpen.value = ''
  menuConv.value = null
}
onMounted(() => document.addEventListener('click', closeMenu))
onBeforeUnmount(() => document.removeEventListener('click', closeMenu))

const deleting = ref<string>('')
async function onDelete(c: Conversation, ev: Event) {
  ev.stopPropagation()
  menuOpen.value = ''
  if (deleting.value) return
  if (!window.confirm(`删除对话「${displayTitle(c)}」？\n对话和消息会删除，扫描成果（漏洞/图）保留。`)) return
  deleting.value = c.ID
  try {
    await deleteConversation(c.ID)
    items.value = items.value.filter((x) => x.ID !== c.ID)
    emit('deleted', c.ID)
  } catch {
    window.alert('删除失败，请重试')
  } finally {
    deleting.value = ''
  }
}
</script>

<template>
  <aside class="conv-list">
    <div class="cl-head">
      <button class="new-conv" @click="emit('new')">+ 新对话</button>
      <button class="refresh" title="刷新列表" @click="refresh">↻</button>
    </div>
    <ul>
      <li
        v-for="c in items"
        :key="c.ID"
        :class="{ active: c.ID === props.activeId }"
        @click="emit('select', c.ID)"
      >
        <span class="cl-dot" :class="'dot-' + statusMeta(c).key" :title="statusMeta(c).label" />
        <div class="cl-body">
          <div class="cl-title" :title="fullTitle(c)">{{ displayTitle(c) }}</div>
          <div class="cl-meta">
            <span class="cl-status" :class="'st-' + statusMeta(c).key">{{ statusMeta(c).label }}</span>
            <span class="cl-time" :title="fullTime(c.CreatedAt)">{{ relativeTime(c.CreatedAt) }}</span>
          </div>
        </div>
        <div class="cl-actions">
          <button class="cl-more" title="更多" @click="toggleMenu(c, $event)">⋯</button>
        </div>
      </li>
      <li v-if="!items.length" class="cl-empty">暂无对话</li>
    </ul>
    <!-- ⋯ 菜单 Teleport 到 body：fixed 视口定位，不被 ul overflow 裁剪 -->
    <Teleport to="body">
      <div v-if="menuConv" class="cl-menu" :style="menuStyle" @click.stop>
        <button
          class="cl-menu-item danger"
          :disabled="deleting === menuConv.ID"
          @click="onDelete(menuConv, $event)"
        >
          🗑 删除对话
        </button>
      </div>
    </Teleport>
  </aside>
</template>

<style scoped>
.conv-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
.cl-head {
  display: flex;
  gap: 6px;
}
.new-conv {
  flex: 1;
  padding: 8px;
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: 8px;
  font-weight: 600;
  cursor: pointer;
}
.refresh {
  width: 36px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--muted);
  cursor: pointer;
}
ul {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  overflow-y: auto;
}
li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
  transition: background var(--duration-fast, 150ms);
}
li:hover {
  background: var(--surface-2);
}
li.active {
  background: var(--surface-2);
  box-shadow: inset 2px 0 0 var(--accent);
}
.cl-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.cl-body {
  flex: 1;
  min-width: 0;
}
.cl-title {
  font-size: 13px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cl-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 2px;
}
.cl-status {
  font-size: 11px;
  font-weight: 600;
}
.cl-time {
  font-size: 11px;
  color: var(--muted);
  font-family: var(--mono);
}
/* 状态文字色（只 color） */
.st-active {
  color: #34d399;
}
.st-done {
  color: #38bdf8;
}
.st-aborted {
  color: #94a3b8;
}
.st-idle {
  color: #64748b;
}
/* 状态色点（只 background；进行中绿脉冲） */
.dot-active {
  background: #34d399;
  animation: cl-pulse 1.6s ease-in-out infinite;
}
.dot-done {
  background: #38bdf8;
}
.dot-aborted {
  background: #94a3b8;
}
.dot-idle {
  background: #64748b;
}
@keyframes cl-pulse {
  50% {
    opacity: 0.4;
  }
}
/* ⋯ 更多菜单 */
.cl-actions {
  position: relative;
  flex-shrink: 0;
}
.cl-more {
  width: 24px;
  height: 24px;
  border: none;
  background: transparent;
  color: var(--muted);
  border-radius: 6px;
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  opacity: 0;
  transition: opacity var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}
li:hover .cl-more,
.cl-more:focus {
  opacity: 1;
}
.cl-more:hover {
  background: var(--surface);
}
.cl-menu {
  position: fixed; /* Teleport 到 body + JS 视口坐标定位（top/left 由 menuStyle 注入） */
  z-index: 1000;
  min-width: 132px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: var(--shadow);
  padding: 4px;
}
.cl-menu-item {
  display: block;
  width: 100%;
  text-align: left;
  padding: 6px 10px;
  border: none;
  background: transparent;
  color: var(--text);
  font-size: 12px;
  border-radius: 6px;
  cursor: pointer;
}
.cl-menu-item:hover {
  background: var(--surface-2);
}
.cl-menu-item.danger:hover {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}
.cl-menu-item:disabled {
  opacity: 0.5;
  cursor: default;
}
.cl-empty {
  color: var(--muted);
  font-size: 12px;
  justify-content: center;
  cursor: default;
}
.cl-empty:hover {
  background: transparent;
}
</style>
