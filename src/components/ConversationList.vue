<script setup lang="ts">
// 对话侧栏：挂载时拉对话列表，点击向上抛选中 ID；顶部「+ 新对话」抛 new。
// 列表项展示状态点 + 标题 + 时间，hover 出删除按钮，当前选中高亮（对齐 ChatGPT/Claude 侧栏惯例）。
import { onMounted, ref } from 'vue'
import { listConversations, deleteConversation } from '../api/client'
import type { Conversation } from '../api/types'
import { clockTime, dayLabel } from '../lib/format'

const props = defineProps<{ activeId?: string }>()
const items = ref<Conversation[]>([])
const emit = defineEmits<{ select: [convID: string]; new: []; deleted: [convID: string] }>()

async function refresh() {
  items.value = await listConversations()
}
onMounted(refresh)
defineExpose({ refresh })

// 状态 → 中文标签 + 色点。active=进行中(绿脉冲)/completed=已完成(蓝)/aborted=已中止(灰)。
function statusMeta(s: string): { label: string; cls: string } {
  if (s === 'active') return { label: '进行中', cls: 'st-active' }
  if (s === 'completed') return { label: '已完成', cls: 'st-done' }
  if (s === 'aborted') return { label: '已中止', cls: 'st-aborted' }
  return { label: s || '—', cls: 'st-idle' }
}

// 标题：优先 Title，去掉「我要扫描」前缀 + 截取 host 让列表更易读；空则回退短 id。
function displayTitle(c: Conversation): string {
  const raw = (c.Title || '').replace(/^我要扫描\s*/, '').trim()
  if (!raw) return c.ID.slice(0, 8)
  const host = raw.match(/https?:\/\/([^/\s]+)/)?.[1]
  return host ? host + raw.replace(/https?:\/\/[^/\s]+/, '').slice(0, 24) : raw.slice(0, 40)
}

// 列表项时间：今天显示时分，否则显示日期标签。
function itemTime(iso: string): string {
  const label = dayLabel(iso)
  return label === '今天' ? clockTime(iso) : label
}

const deleting = ref<string>('')
async function onDelete(c: Conversation, ev: Event) {
  ev.stopPropagation() // 不触发选中
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
        <span class="cl-dot" :class="'dot-' + statusMeta(c.Status).cls" :title="statusMeta(c.Status).label" />
        <div class="cl-body">
          <div class="cl-title">{{ displayTitle(c) }}</div>
          <div class="cl-meta">
            <span class="cl-status" :class="statusMeta(c.Status).cls">{{ statusMeta(c.Status).label }}</span>
            <span class="cl-time">{{ itemTime(c.CreatedAt) }}</span>
          </div>
        </div>
        <button
          class="cl-del"
          :disabled="deleting === c.ID"
          title="删除对话"
          @click="onDelete(c, $event)"
        >
          ✕
        </button>
      </li>
      <li v-if="!items.length" class="cl-empty">暂无对话</li>
    </ul>
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
/* 状态文字：只用 color（进行中绿 / 已完成蓝 / 已中止灰 / 其它灰） */
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
/* 状态色点：纯 background（进行中绿脉冲），与文字 class 分开避免互相污染 */
.dot-st-active {
  background: #34d399;
  animation: cl-pulse 1.6s ease-in-out infinite;
}
.dot-st-done {
  background: #38bdf8;
}
.dot-st-aborted {
  background: #94a3b8;
}
.dot-st-idle {
  background: #64748b;
}
@keyframes cl-pulse {
  50% {
    opacity: 0.4;
  }
}
.cl-del {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  border: none;
  background: transparent;
  color: var(--muted);
  border-radius: 6px;
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}
li:hover .cl-del {
  opacity: 1;
}
.cl-del:hover {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}
.cl-del:disabled {
  cursor: default;
  opacity: 0.4;
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
